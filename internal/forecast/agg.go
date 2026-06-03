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
	Hours       map[string]float64 // projectID -> hours
	Total       float64            // sum over projects
}

// WeekView aggregates a single ISO week (Mon-Fri).
type WeekView struct {
	Year           int
	Week           int
	Label          string
	Days           []DayCell
	ProjectTotals  map[string]float64
	Total          float64
	TargetHours    float64
	UtilizationPct float64
	PrevWeek       int
	NextWeek       int
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

// entryIndex maps "date|projectId" to hours for quick lookup.
func entryIndex(entries []models.Entry) map[string]float64 {
	idx := make(map[string]float64, len(entries))
	for _, e := range entries {
		idx[e.Date+"|"+e.ProjectID] += e.Hours
	}
	return idx
}

// BuildWeek assembles the Mon-Fri view for one ISO week.
func BuildWeek(d models.Data, cal *holidays.Calendar, week int) WeekView {
	year := d.Settings.Year
	monday := MondayOfISOWeek(year, week)
	idx := entryIndex(d.Entries)

	wv := WeekView{
		Year:          year,
		Week:          week,
		Label:         fmt.Sprintf("KW %02d", week),
		ProjectTotals: map[string]float64{},
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
		}
		for _, p := range d.Projects {
			h := idx[iso+"|"+p.ID]
			if h != 0 {
				cell.Hours[p.ID] = h
				cell.Total += h
				wv.ProjectTotals[p.ID] += h
			}
		}
		wv.Total += cell.Total
		wv.Days = append(wv.Days, cell)
	}

	if wv.TargetHours > 0 {
		wv.UtilizationPct = round1(wv.Total / wv.TargetHours * 100)
	}
	return wv
}

// ProjectSummary describes budget consumption for one project.
type ProjectSummary struct {
	Project       models.Project
	Consumed      float64
	Remaining     float64
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
		if e.ProjectID != projectID {
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
