package forecast

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

type MonthEvent struct {
	ProjectID, Name, Color, Basis string
	Hours                         float64
	Vacation, Estimated, Outside  bool
}

type MonthDay struct {
	Date, Label, Holiday              string
	InMonth, InYear, Today, Past      bool
	Weekend                           bool
	Estimated                         bool
	Capacity, Vacation, Stored, Total float64
	Over                              float64
	Events                            []MonthEvent
}

type MonthWeek struct {
	ISOWeek, FYWeek                        int
	Label                                  string
	Days                                   []MonthDay
	Stored, Capacity, Vacation, Work, Free float64
	Over, Unallocated                      float64
	WeekendStored                          float64
	Pending                                []MonthEvent
	Projects                               []MonthEvent
}

type MonthPlan struct {
	Month, Label, Prev, Next, HistoryFrom, HistoryTo string
	Weeks                                            []MonthWeek
}

// BuildMonthPlan is a read-only alternative distribution of existing weekly
// forecast totals. Past entries and all vacation entries retain their dates.
func BuildMonthPlan(d models.Data, cal *holidays.Calendar, month, now time.Time) MonthPlan {
	return buildMonthPlan(d, cal, month, now, true)
}

// BuildStoredMonthPlan renders the actual daily distribution without estimating
// it again, including a validated AI preview or an explicitly saved plan.
func BuildStoredMonthPlan(d models.Data, cal *holidays.Calendar, month, now time.Time) MonthPlan {
	return buildMonthPlan(d, cal, month, now, false)
}

func buildMonthPlan(d models.Data, cal *holidays.Calendar, month, now time.Time, estimate bool) MonthPlan {
	fyStart, fyEnd := FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	if month.Before(fyStart) {
		month = fyStart
	}
	if month.After(fyEnd) {
		month = time.Date(fyEnd.Year(), fyEnd.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	today := now.UTC().Format("2006-01-02")
	historyEnd := mondayOf(now.UTC())
	historyStart := historyEnd.AddDate(0, 0, -7*monthHistoryWeeks)
	plan := MonthPlan{
		Month: month.Format("2006-01"), Label: fmt.Sprintf("%s %d", monthNames[month.Month()-1], month.Year()),
		HistoryFrom: historyStart.Format("02.01.2006"),
		HistoryTo:   historyEnd.AddDate(0, 0, -1).Format("02.01.2006"),
	}
	if month.After(fyStart) {
		plan.Prev = month.AddDate(0, -1, 0).Format("2006-01")
	}
	if next := month.AddDate(0, 1, 0); !next.After(fyEnd) {
		plan.Next = next.Format("2006-01")
	}

	projects := SortedProjects(d.Projects)
	index := hoursIndex(d.Entries)
	var histories map[string]monthHistory
	if estimate {
		histories = buildMonthHistories(projects, index, historyEnd, d.Settings.FederalState)
	}
	end := month.AddDate(0, 1, 0)
	for monday := mondayOf(month); monday.Before(end); monday = monday.AddDate(0, 0, 7) {
		_, isoWeek := monday.ISOWeek()
		anchor := monday
		if anchor.Before(fyStart) {
			anchor = fyStart
		}
		w := MonthWeek{
			ISOWeek: isoWeek, FYWeek: FYWeekIndexOf(d.Settings.Year, d.Settings.FiscalYearStartMonth, anchor),
			Label: monday.Format("02.01.") + " – " + monday.AddDate(0, 0, 6).Format("02.01."),
		}
		remaining := make(map[string]float64)
		projectHours := make(map[string]float64)
		for i := 0; i < 7; i++ {
			date := monday.AddDate(0, 0, i)
			iso := date.Format("2006-01-02")
			day := MonthDay{
				Date: iso, Label: monthWeekdayNames[i] + " " + date.Format("02.01."), Holiday: cal.Name(iso),
				InMonth: date.Month() == month.Month(), InYear: !date.Before(fyStart) && !date.After(fyEnd),
				Today: iso == today, Past: iso < today, Weekend: i >= 5,
				Estimated: estimate && iso >= today,
			}
			if day.InYear {
				if !day.Weekend && day.Holiday == "" {
					day.Capacity = HolidayDayHours
				}
				for _, p := range projects {
					h := index[iso+"|"+p.ID]
					if h <= 0 {
						continue
					}
					day.Stored += h
					if p.IsVacation() {
						day.Vacation += h
					} else {
						w.Work += h
						projectHours[p.ID] += h
					}
					if estimate && !day.Past && !p.IsVacation() {
						remaining[p.ID] += h
						continue
					}
					day.Events = append(day.Events, monthEvent(p, h, false, "", iso))
					day.Total += h
				}
				w.Stored += day.Stored
				if day.Weekend {
					w.WeekendStored += day.Stored
				}
				w.Capacity += day.Capacity
				w.Vacation += day.Vacation
			}
			w.Days = append(w.Days, day)
		}
		if estimate {
			distributeMonthWeek(&w, projects, remaining, histories)
		}
		for _, p := range projects {
			if h := projectHours[p.ID]; h > 0 {
				w.Projects = append(w.Projects, monthEvent(p, h, false, "", ""))
			}
		}
		w.Free = math.Max(0, w.Capacity-w.Stored)
		w.Over = math.Max(0, w.Stored-w.Capacity)
		for i := range w.Days {
			day := &w.Days[i]
			day.Over = math.Max(0, day.Total-day.Capacity)
		}
		plan.Weeks = append(plan.Weeks, w)
	}
	return plan
}

func monthEvent(p models.Project, hours float64, estimated bool, basis, date string) MonthEvent {
	return MonthEvent{
		ProjectID: p.ID, Name: p.Name, Color: p.Color, Hours: hours,
		Vacation: p.IsVacation(), Estimated: estimated, Basis: basis,
		Outside: date != "" && !p.Bookable(date),
	}
}

func monthDayAvailable(day MonthDay, p models.Project) bool {
	return day.InYear && !day.Past && !day.Weekend && day.Holiday == "" && p.Bookable(day.Date) && day.Capacity-day.Total > 1e-9
}

func distributeMonthWeek(w *MonthWeek, projects []models.Project, remaining map[string]float64, histories map[string]monthHistory) {
	var candidates []models.Project
	for _, p := range projects {
		if remaining[p.ID] > 0 {
			candidates = append(candidates, p)
		}
	}
	// Narrow booking windows go first so flexible projects do not consume their
	// only available days. Strong weekday preferences precede flexible patterns.
	eligible := func(p models.Project) int {
		n := 0
		for _, day := range w.Days {
			if monthDayAvailable(day, p) {
				n++
			}
		}
		return n
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := eligible(candidates[i]), eligible(candidates[j])
		if a != b {
			return a < b
		}
		peak := func(p models.Project) float64 {
			var max float64
			for _, weight := range histories[groupKey(p)].weights {
				max = math.Max(max, weight)
			}
			return max
		}
		if a, b := peak(candidates[i]), peak(candidates[j]); a != b {
			return a > b
		}
		return candidates[i].ID < candidates[j].ID
	})
	for _, p := range candidates {
		left := remaining[p.ID]
		history := histories[groupKey(p)]
		allocated := make([]float64, len(w.Days))
		// Each capped pass fills at least one day; an uncapped pass consumes
		// the remaining hours. At most seven passes are therefore needed.
		for pass := 0; pass < len(w.Days) && left > 1e-9; pass++ {
			weights := make([]float64, len(w.Days))
			var sum float64
			for i, day := range w.Days {
				if monthDayAvailable(day, p) {
					weights[i] = (day.Capacity - day.Total) * history.weights[i]
					sum += weights[i]
				}
			}
			if sum <= 1e-9 {
				for i, day := range w.Days {
					if monthDayAvailable(day, p) {
						weights[i] = day.Capacity - day.Total
						sum += weights[i]
					}
				}
			}
			if sum <= 1e-9 {
				break
			}
			requested := left
			for i := range w.Days {
				h := math.Min(w.Days[i].Capacity-w.Days[i].Total, requested*weights[i]/sum)
				if h > 0 {
					allocated[i] += h
					w.Days[i].Total += h
					left -= h
				}
			}
		}
		for i, h := range allocated {
			if h > 1e-9 {
				w.Days[i].Events = append(w.Days[i].Events, monthEvent(p, h, true, history.basis, w.Days[i].Date))
			}
		}
		if left > 1e-9 {
			w.Unallocated += left
			w.Pending = append(w.Pending, monthEvent(p, left, true, history.basis, ""))
		}
	}
}
