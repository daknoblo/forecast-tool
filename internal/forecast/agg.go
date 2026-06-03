package forecast

import (
	"fmt"
	"sort"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// DayCell holds the forecast for a single day across all projects.
type DayCell struct {
	Date        string             // YYYY-MM-DD
	WeekdayName string             // Mo, Di, ...
	InYear      bool               // belongs to the configured calendar year
	IsHoliday   bool               // public holiday
	HolidayName string             // holiday label, if any
	Hours       map[string]float64 // projectID -> forecast hours
	Actual      map[string]float64 // projectID -> actual (booked) hours
	Total       float64            // forecast sum over projects
	ActualTotal float64            // actual sum over projects
}

// WeekView aggregates a single ISO week (Mon-Fri).
type WeekView struct {
	Year                 int
	Week                 int
	Label                string
	Days                 []DayCell
	ProjectTotals        map[string]float64
	ActualTotals         map[string]float64
	Total                float64
	ActualTotal          float64
	TargetHours          float64
	UtilizationPct       float64
	ActualUtilizationPct float64
	PrevWeek             int
	NextWeek             int
}

var weekdayNames = []string{"Mo", "Di", "Mi", "Do", "Fr"}

// MondayOfISOWeek returns the Monday (00:00 UTC) of the given ISO week.
func MondayOfISOWeek(year, week int) time.Time {
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	iso := int(jan4.Weekday())
	if iso == 0 {
		iso = 7
	}
	week1Monday := jan4.AddDate(0, 0, -(iso - 1))
	return week1Monday.AddDate(0, 0, (week-1)*7)
}

// WeeksInYear returns the number of ISO weeks (52 or 53) in the given year.
func WeeksInYear(year int) int {
	dec28 := time.Date(year, time.December, 28, 0, 0, 0, 0, time.UTC)
	_, w := dec28.ISOWeek()
	return w
}

// entryKind returns the effective kind of an entry, treating an empty kind as
// a forecast for backwards compatibility.
func entryKind(e models.Entry) string {
	if e.Kind == models.KindActual {
		return models.KindActual
	}
	return models.KindForecast
}

// kindIndex maps "date|projectId" to hours for entries of the given kind.
func kindIndex(entries []models.Entry, kind string) map[string]float64 {
	idx := make(map[string]float64, len(entries))
	for _, e := range entries {
		if entryKind(e) == kind {
			idx[e.Date+"|"+e.ProjectID] += e.Hours
		}
	}
	return idx
}

// BuildWeek assembles the Mon-Fri view for one ISO week.
func BuildWeek(d models.Data, cal *holidays.Calendar, week int) WeekView {
	year := d.Settings.Year
	monday := MondayOfISOWeek(year, week)
	fidx := kindIndex(d.Entries, models.KindForecast)
	aidx := kindIndex(d.Entries, models.KindActual)

	wv := WeekView{
		Year:          year,
		Week:          week,
		Label:         fmt.Sprintf("KW %02d", week),
		ProjectTotals: map[string]float64{},
		ActualTotals:  map[string]float64{},
		TargetHours:   d.Settings.WeeklyTargetHours,
		PrevWeek:      week - 1,
		NextWeek:      week + 1,
	}

	for i := 0; i < 5; i++ {
		day := monday.AddDate(0, 0, i)
		iso := day.Format("2006-01-02")
		cell := DayCell{
			Date:        iso,
			WeekdayName: weekdayNames[i],
			InYear:      day.Year() == year,
			IsHoliday:   cal.IsHoliday(iso),
			HolidayName: cal.Name(iso),
			Hours:       map[string]float64{},
			Actual:      map[string]float64{},
		}
		for _, p := range d.Projects {
			if h := fidx[iso+"|"+p.ID]; h != 0 {
				cell.Hours[p.ID] = h
				cell.Total += h
				wv.ProjectTotals[p.ID] += h
			}
			if a := aidx[iso+"|"+p.ID]; a != 0 {
				cell.Actual[p.ID] = a
				cell.ActualTotal += a
				wv.ActualTotals[p.ID] += a
			}
		}
		wv.Total += cell.Total
		wv.ActualTotal += cell.ActualTotal
		wv.Days = append(wv.Days, cell)
	}

	if wv.TargetHours > 0 {
		wv.UtilizationPct = round1(wv.Total / wv.TargetHours * 100)
		wv.ActualUtilizationPct = round1(wv.ActualTotal / wv.TargetHours * 100)
	}
	return wv
}

// ProjectSummary describes budget consumption for one project.
type ProjectSummary struct {
	Project        models.Project
	Consumed       float64
	Remaining      float64
	UtilizationPct float64
}

// YearSummary aggregates all projects and weekly totals for the year.
type YearSummary struct {
	Projects   []ProjectSummary
	TotalHours float64
	WeekTotals []WeekTotal
}

// WeekTotal is the summed forecast for a single week (all projects).
type WeekTotal struct {
	Week           int
	Hours          float64
	TargetHours    float64
	UtilizationPct float64
}

// BuildYearSummary computes per-project consumption and weekly totals.
func BuildYearSummary(d models.Data) YearSummary {
	consumed := map[string]float64{}
	for _, e := range d.Entries {
		if entryKind(e) != models.KindForecast {
			continue
		}
		consumed[e.ProjectID] += e.Hours
	}

	ys := YearSummary{}
	for _, p := range d.Projects {
		c := consumed[p.ID]
		rem := p.BudgetHours - c
		util := 0.0
		if p.BudgetHours > 0 {
			util = round1(c / p.BudgetHours * 100)
		}
		ys.Projects = append(ys.Projects, ProjectSummary{
			Project:        p,
			Consumed:       round1(c),
			Remaining:      round1(rem),
			UtilizationPct: util,
		})
		ys.TotalHours += c
	}
	ys.TotalHours = round1(ys.TotalHours)

	// weekly totals
	weeks := WeeksInYear(d.Settings.Year)
	weekSum := make(map[int]float64)
	for _, e := range d.Entries {
		if entryKind(e) != models.KindForecast {
			continue
		}
		t, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			continue
		}
		_, w := t.ISOWeek()
		weekSum[w] += e.Hours
	}
	for w := 1; w <= weeks; w++ {
		util := 0.0
		if d.Settings.WeeklyTargetHours > 0 {
			util = round1(weekSum[w] / d.Settings.WeeklyTargetHours * 100)
		}
		ys.WeekTotals = append(ys.WeekTotals, WeekTotal{
			Week:           w,
			Hours:          round1(weekSum[w]),
			TargetHours:    d.Settings.WeeklyTargetHours,
			UtilizationPct: util,
		})
	}
	return ys
}

// BurnPoint is a single data point of a project burn-down curve.
type BurnPoint struct {
	Week      int
	Remaining float64
}

// BuildBurndown returns the remaining-budget curve for one project over the year.
func BuildBurndown(d models.Data, projectID string, budget float64) []BurnPoint {
	weeks := WeeksInYear(d.Settings.Year)
	weekSum := make(map[int]float64)
	for _, e := range d.Entries {
		if e.ProjectID != projectID || entryKind(e) != models.KindForecast {
			continue
		}
		t, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			continue
		}
		_, w := t.ISOWeek()
		weekSum[w] += e.Hours
	}
	points := make([]BurnPoint, 0, weeks+1)
	points = append(points, BurnPoint{Week: 0, Remaining: round1(budget)})
	cum := 0.0
	for w := 1; w <= weeks; w++ {
		cum += weekSum[w]
		points = append(points, BurnPoint{Week: w, Remaining: round1(budget - cum)})
	}
	return points
}

var monthNames = []string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

// PeriodStat captures target vs. forecast/actual for one period (quarter/month).
type PeriodStat struct {
	Label       string
	Target      float64 // proportional target by working days
	Forecast    float64 // planned hours in the period
	Actual      float64 // actually booked hours in the period
	Projected   float64 // effective hours (past = actual, future = forecast)
	PctOfTarget float64 // projected / target * 100
}

// GoalSummary tracks fiscal-year target attainment based on actuals so far plus
// the remaining forecast.
type GoalSummary struct {
	HasTarget         bool
	TargetHours       float64
	ActualTotal       float64 // sum of all actual entries
	ForecastTotal     float64 // sum of all forecast entries
	ForecastRemaining float64 // forecast for the current and future weeks
	Projected         float64 // actual (past) + forecast (current+future)
	Remaining         float64 // target - projected
	PctProjected      float64 // projected / target * 100
	PctActual         float64 // actual / target * 100
	WorkingDaysYear   int
	WorkingDaysDone   int
	TargetPerDay      float64
	TargetPerWeek     float64 // target for a full 5-day working week
	TargetPerMonth    float64 // average per month
	TargetPerQuarter  float64 // average per quarter
	Quarters          []PeriodStat
	Months            []PeriodStat
}

// BuildGoalSummary computes fiscal-year goal attainment. Days before the Monday
// of the current ISO week count as "past" and use actual hours; the current and
// future weeks use the forecast. Period targets are derived proportionally from
// the number of working days (Mon-Fri minus holidays).
func BuildGoalSummary(d models.Data, cal *holidays.Calendar) GoalSummary {
	year := d.Settings.Year
	target := d.Settings.FiscalYearTargetHours
	curMonday := MondayOfISOWeek(year, CurrentWeek(year))

	fByDate := map[string]float64{}
	aByDate := map[string]float64{}
	for _, e := range d.Entries {
		if entryKind(e) == models.KindActual {
			aByDate[e.Date] += e.Hours
		} else {
			fByDate[e.Date] += e.Hours
		}
	}

	gs := GoalSummary{TargetHours: round1(target), HasTarget: target > 0}
	quarters := make([]PeriodStat, 4)
	months := make([]PeriodStat, 12)
	var qWork [4]int
	var mWork [12]int

	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		iso := day.Format("2006-01-02")
		m := int(day.Month()) - 1
		q := m / 3
		wd := day.Weekday()
		working := wd != time.Saturday && wd != time.Sunday && !cal.IsHoliday(iso)
		past := day.Before(curMonday)
		if working {
			gs.WorkingDaysYear++
			qWork[q]++
			mWork[m]++
			if past {
				gs.WorkingDaysDone++
			}
		}

		f := fByDate[iso]
		a := aByDate[iso]
		eff := f
		if past {
			eff = a
		}

		gs.ActualTotal += a
		gs.ForecastTotal += f
		if !past {
			gs.ForecastRemaining += f
		}
		gs.Projected += eff

		quarters[q].Actual += a
		quarters[q].Forecast += f
		quarters[q].Projected += eff
		months[m].Actual += a
		months[m].Forecast += f
		months[m].Projected += eff
	}

	perDay := 0.0
	if gs.WorkingDaysYear > 0 {
		perDay = target / float64(gs.WorkingDaysYear)
	}
	gs.TargetPerDay = round1(perDay)
	gs.TargetPerWeek = round1(perDay * 5)
	gs.TargetPerMonth = round1(target / 12)
	gs.TargetPerQuarter = round1(target / 4)

	for i := 0; i < 4; i++ {
		quarters[i].Label = fmt.Sprintf("Q%d", i+1)
		quarters[i].Target = round1(perDay * float64(qWork[i]))
		quarters[i].Actual = round1(quarters[i].Actual)
		quarters[i].Forecast = round1(quarters[i].Forecast)
		quarters[i].Projected = round1(quarters[i].Projected)
		if quarters[i].Target > 0 {
			quarters[i].PctOfTarget = round1(quarters[i].Projected / quarters[i].Target * 100)
		}
	}
	for i := 0; i < 12; i++ {
		months[i].Label = monthNames[i]
		months[i].Target = round1(perDay * float64(mWork[i]))
		months[i].Actual = round1(months[i].Actual)
		months[i].Forecast = round1(months[i].Forecast)
		months[i].Projected = round1(months[i].Projected)
		if months[i].Target > 0 {
			months[i].PctOfTarget = round1(months[i].Projected / months[i].Target * 100)
		}
	}

	gs.ActualTotal = round1(gs.ActualTotal)
	gs.ForecastTotal = round1(gs.ForecastTotal)
	gs.ForecastRemaining = round1(gs.ForecastRemaining)
	gs.Projected = round1(gs.Projected)
	gs.Remaining = round1(target - gs.Projected)
	if target > 0 {
		gs.PctProjected = round1(gs.Projected / target * 100)
		gs.PctActual = round1(gs.ActualTotal / target * 100)
	}
	gs.Quarters = quarters
	gs.Months = months
	return gs
}

// CurrentWeek returns the ISO week for today's date clamped to the year.
func CurrentWeek(year int) int {
	now := time.Now()
	if now.Year() != year {
		return 1
	}
	_, w := now.ISOWeek()
	return w
}

// SortedProjects returns projects sorted by name for stable display.
func SortedProjects(ps []models.Project) []models.Project {
	out := append([]models.Project(nil), ps...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func round1(f float64) float64 {
	return float64(int64(f*10+sign(f)*0.5)) / 10
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
