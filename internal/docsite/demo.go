// Package docsite builds the public documentation site: it seeds a demo data
// set, renders the running application into a static, clickable snapshot,
// captures screenshots and turns the repository's Markdown files into HTML.
package docsite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// DemoState is the federal state the demo data is planned against.
const DemoState = "SN"

// demoStartMonth is the fiscal-year start month of the demo document (July).
const demoStartMonth = 7

// demoForecastWeeks is how far beyond today the demo plans hours. Everything
// before today reads as booked, everything after as forecast, so the demo shows
// both sides of every chart.
const demoForecastWeeks = 10

// demoProject describes one project of the demo document. The window is given
// in whole months relative to the fiscal-year start; -1 means "open".
type demoProject struct {
	assignmentID string
	name         string
	budget       float64
	color        string
	startMonth   int
	endMonth     int
	// share weights the project when hours are distributed over a day.
	share int
	// carryOver, when > 0, additionally creates the same assignment in the
	// previous fiscal year and books that many hours on it.
	carryOver float64
}

// demoProjects are deliberately short-named: the hours-flow chart renders the
// project labels into a fixed left padding, where a long name would be clipped.
var demoProjects = []demoProject{
	{assignmentID: "5641245", name: "Cloud-Migration", budget: 480, color: "#2563eb", startMonth: 0, endMonth: 8, share: 5, carryOver: 130},
	{assignmentID: "5698112", name: "Betrieb & Support", budget: 260, color: "#0d9488", startMonth: -1, endMonth: -1, share: 4},
	{assignmentID: "5701330", name: "Data-Warehouse", budget: 340, color: "#d97706", startMonth: 2, endMonth: 11, share: 4},
	{assignmentID: "5712004", name: "Security-Audit", budget: 120, color: "#dc2626", startMonth: 5, endMonth: 7, share: 3},
	{assignmentID: "5720881", name: "Weiterbildung", budget: 90, color: "#7c3aed", startMonth: -1, endMonth: -1, share: 1},
}

// WriteDemoData builds a deterministic demo document and writes it to
// dir/data.json. It is anchored on `today`, so the demo always shows a fiscal
// year in progress: booked hours in the past, forecast hours ahead.
func WriteDemoData(dir string, today time.Time) error {
	d := buildDemoData(today)
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "data.json"), b, 0o600)
}

func buildDemoData(today time.Time) models.Data {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	year := forecast.FiscalYearOf(today, demoStartMonth)
	prev := year - 1

	d := models.DefaultData(year)
	d.Settings.FederalState = DemoState
	d.Settings.FiscalYearStartMonth = demoStartMonth
	d.Settings.WeeklyTargetHours = 40
	d.Settings.AI = models.AISettings{
		Endpoint:   "https://demo.example.com",
		Deployment: "model-router",
		APIVersion: "2024-10-21",
	}
	fySettings := models.FiscalYearSettings{
		VacationDays:      30,
		StandardTaskLabel: "Meetings & Orga",
		StandardTaskHours: 250,
	}
	d.FiscalYears = map[int]models.FiscalYearSettings{year: fySettings, prev: fySettings}

	start, end := forecast.FiscalYear(year, demoStartMonth)
	for _, dp := range demoProjects {
		d.Projects = append(d.Projects, models.Project{
			ID:           "demo-" + dp.assignmentID,
			AssignmentID: dp.assignmentID,
			Name:         dp.name,
			BudgetHours:  dp.budget,
			Color:        dp.color,
			Active:       true,
			FiscalYear:   year,
			StartDate:    monthOffsetISO(start, dp.startMonth, false),
			EndDate:      monthOffsetISO(start, dp.endMonth, true),
		})
	}
	models.EnsureVacationProject(&d, year)

	// The previous fiscal year only exists to demonstrate the carry-over of a
	// continued assignment, so it gets the projects but no vacation planning.
	for _, dp := range demoProjects {
		if dp.carryOver <= 0 {
			continue
		}
		d.Projects = append(d.Projects, models.Project{
			ID:           "demo-prev-" + dp.assignmentID,
			AssignmentID: dp.assignmentID,
			Name:         dp.name,
			BudgetHours:  dp.budget,
			Color:        dp.color,
			Active:       false,
			FiscalYear:   prev,
		})
	}

	d.Entries = demoEntries(d.Projects, today, year, start, end)
	d.Entries = append(d.Entries, carryOverEntries(start)...)
	return d
}

// monthOffsetISO returns the ISO date `months` months after the fiscal-year
// start, either its first day or - for an end bound - its last day. A negative
// offset means "no bound" (empty string).
func monthOffsetISO(fyStart time.Time, months int, endOfMonth bool) string {
	if months < 0 {
		return ""
	}
	t := fyStart.AddDate(0, months, 0)
	if endOfMonth {
		t = t.AddDate(0, 1, 0).AddDate(0, 0, -1)
	}
	return t.Format("2006-01-02")
}

// demoEntries books hours on every weekday from the fiscal-year start up to a
// few weeks past today, skipping public holidays and filling two vacation
// blocks. The pattern is derived from a hash of the date, so the result is
// identical on every run for a given day.
func demoEntries(projects []models.Project, today time.Time, year int, fyStart, fyEnd time.Time) []models.Entry {
	cal := holidays.Get(year, DemoState)
	vacationID := models.VacationProjectID(year)
	vacation := vacationWeeks(today, fyStart, fyEnd)

	// End on a Friday, otherwise the horizon cuts a week in half and the last
	// Sankey column shows a stub.
	last := mondayOf(today.AddDate(0, 0, demoForecastWeeks*7)).AddDate(0, 0, 4)
	if last.After(fyEnd) {
		last = fyEnd
	}

	var out []models.Entry
	for day := fyStart; !day.After(last); day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		iso := day.Format("2006-01-02")
		if cal.IsHoliday(iso) {
			continue
		}
		if vacation[iso] {
			out = append(out, models.Entry{Date: iso, ProjectID: vacationID, Hours: 8})
			continue
		}
		out = append(out, dayEntries(projects, year, iso)...)
	}
	return out
}

// dayHourPattern is the daily total booked on a demo day, picked by a hash of
// the date. The spread makes the utilization traffic light and the free-capacity
// chart show every state.
var dayHourPattern = []float64{8, 8, 6, 9, 8, 10, 7, 8, 4, 8.5}

// dayEntries splits a day's total over the projects whose booking window covers
// that day, weighted by their share.
func dayEntries(projects []models.Project, year int, iso string) []models.Entry {
	type candidate struct {
		id    string
		share int
	}
	var cands []candidate
	for _, dp := range demoProjects {
		p, ok := projectByAssignment(projects, year, dp.assignmentID)
		if !ok || !p.Bookable(iso) {
			continue
		}
		cands = append(cands, candidate{id: p.ID, share: dp.share})
	}
	if len(cands) == 0 {
		return nil
	}

	h := hash(iso)
	total := dayHourPattern[h%uint32(len(dayHourPattern))]
	// Rotate which project leads the day so no single band dominates the Sankey.
	lead := int(h/7) % len(cands)
	cands = append(cands[lead:], cands[:lead]...)
	// Two projects per day keeps the grid readable; a third joins every 4th day.
	take := 2
	if h%4 == 0 {
		take = 3
	}
	if take > len(cands) {
		take = len(cands)
	}
	cands = cands[:take]

	weights := 0
	for _, c := range cands {
		weights += c.share
	}
	out := make([]models.Entry, 0, len(cands))
	left := total
	for i, c := range cands {
		hours := roundHalf(total * float64(c.share) / float64(weights))
		if i == len(cands)-1 || hours > left {
			hours = left
		}
		left -= hours
		if hours <= 0 {
			continue
		}
		out = append(out, models.Entry{Date: iso, ProjectID: c.id, Hours: hours})
	}
	return out
}

// carryOverEntries books the previous fiscal year of every continued assignment
// in the weeks right before the current fiscal year starts. Hours are attributed
// to a fiscal year by their date, which is what the carry-over columns show.
func carryOverEntries(fyStart time.Time) []models.Entry {
	var out []models.Entry
	for _, dp := range demoProjects {
		if dp.carryOver <= 0 {
			continue
		}
		left := dp.carryOver
		day := fyStart.AddDate(0, 0, -1)
		for left > 0 {
			if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
				hours := 8.0
				if hours > left {
					hours = left
				}
				out = append(out, models.Entry{
					Date:      day.Format("2006-01-02"),
					ProjectID: "demo-prev-" + dp.assignmentID,
					Hours:     hours,
				})
				left -= hours
			}
			day = day.AddDate(0, 0, -1)
		}
	}
	return out
}

// vacationWeeks returns the ISO dates covered by the demo vacation: one week
// already taken, one in the week after today - close enough to show up in the
// forecast screenshot and in the default Sankey horizon - plus a bridge day, so
// the vacation band visibly absorbs the other projects and releases them again.
func vacationWeeks(today, fyStart, fyEnd time.Time) map[string]bool {
	out := map[string]bool{}
	mark := func(from time.Time, days int) {
		for i := 0; i < days; i++ {
			day := from.AddDate(0, 0, i)
			if day.Before(fyStart) || day.After(fyEnd) {
				continue
			}
			if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
				continue
			}
			out[day.Format("2006-01-02")] = true
		}
	}
	mark(mondayOf(today.AddDate(0, 0, -35)), 5)
	mark(mondayOf(today.AddDate(0, 0, 7)), 5)
	mark(mondayOf(today.AddDate(0, 0, -14)), 1)
	return out
}

func mondayOf(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -off)
}

func projectByAssignment(projects []models.Project, year int, assignmentID string) (models.Project, bool) {
	for _, p := range projects {
		if p.FiscalYear == year && p.AssignmentID == assignmentID {
			return p, true
		}
	}
	return models.Project{}, false
}

// hash is a small FNV-1a over the string; it replaces a random source so the
// demo document is byte-identical for a given day.
func hash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func roundHalf(f float64) float64 {
	return float64(int(f*2+0.5)) / 2
}
