// Package sample builds the fictional document that the private
// ("presentation") mode renders instead of the real one. Everything a viewer
// could recognise - project names, assignment IDs, budgets and hours - is
// replaced, but the document stays complete, so every page keeps its bars,
// charts, indicators and carry-over figures.
//
// The result is deterministic for a given day and fiscal year: reloading a page
// never reshuffles the demo.
package sample

import (
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// idPrefix namespaces every generated project ID so a demo ID can never collide
// with a real one - a stray write referencing it is dropped as unknown.
const idPrefix = "sample-"

const dateFmt = "2006-01-02"

// forecastWeeks is the minimum number of weeks planned beyond the reference
// day. The sample plans the current half-year completely, but never less than
// this, so the forward-looking charts are never empty.
const forecastWeeks = 10

// vacationDayHours is credited per planned vacation day.
const vacationDayHours = 8.0

// project describes one project of the sample document. The window is given in
// whole months relative to the fiscal-year start; -1 means "unbounded".
type project struct {
	assignmentID string
	name         string
	color        string
	startMonth   int
	endMonth     int
	// share weights the project when a day's hours are split.
	share int
	// active projects appear in the forecast grid; an inactive one releases its
	// unplanned budget and only carries hours from before the reference day.
	active bool
	// carryOver, when > 0, additionally creates the same assignment in the
	// previous fiscal year and books that many hours on it.
	carryOver float64
	// budgetFactor sets the budget as a multiple of the hours actually planned on
	// the assignment. Deriving it instead of fixing it keeps a project over or
	// under its budget at every point of the year, no matter how much is planned
	// yet: < 1 is overbooked, > 1 leaves budget open.
	budgetFactor float64
	// budget is the fallback while the project's window has not opened yet and
	// there is nothing planned to derive from.
	budget float64
}

// projects are deliberately short-named: the hours-flow and Sankey charts render
// the labels into a fixed padding, where a long name would be clipped.
var projects = []project{
	{assignmentID: "5641245", name: "Cloud-Migration", color: "#2563eb", startMonth: 0, endMonth: 8, share: 5, active: true, carryOver: 150, budgetFactor: 1.18, budget: 520},
	{assignmentID: "5698112", name: "Betrieb & Support", color: "#0d9488", startMonth: -1, endMonth: -1, share: 4, active: true, carryOver: 60, budgetFactor: 0.94, budget: 300},
	{assignmentID: "5701330", name: "Data-Plattform", color: "#d97706", startMonth: 2, endMonth: 11, share: 4, active: true, budgetFactor: 1.30, budget: 380},
	{assignmentID: "5712004", name: "Security-Audit", color: "#dc2626", startMonth: 4, endMonth: 8, share: 3, active: true, budgetFactor: 1.12, budget: 120},
	{assignmentID: "5720881", name: "Weiterbildung", color: "#7c3aed", startMonth: -1, endMonth: -1, share: 1, active: true, budgetFactor: 0.85, budget: 90},
	{assignmentID: "5566102", name: "Legacy-Ablösung", color: "#be185d", startMonth: 0, endMonth: 4, share: 3, active: false, carryOver: 180, budgetFactor: 1.06, budget: 240},
	{assignmentID: "5588431", name: "Reporting-Tool", color: "#0891b2", startMonth: 1, endMonth: 5, share: 2, active: false, carryOver: 70, budgetFactor: 1.45, budget: 110},
}

// weekHours is the total booked in an FY week, cycled over the year. The spread
// deliberately crosses every utilization tier (below the burn rate, optimal,
// high, overbooked) so the traffic light and the free-capacity chart show all of
// their states instead of a flat 40 h line. Its average is chosen so a fully
// planned year lands slightly above the fiscal-year goal.
var weekHours = [...]float64{32, 37, 22, 35, 44, 29, 34, 61, 25, 38, 19, 33, 41, 28}

// Data returns the sample document for the given day. Settings and per-fiscal
// year configuration are taken from base, so the demo has the same fiscal year,
// federal state, capacity and traffic-light thresholds as the real document and
// every page keeps its familiar shape. Only projects and entries are invented;
// base itself is never modified.
func Data(now time.Time, base models.Data) models.Data {
	d := base
	startMonth := d.Settings.FiscalYearStartMonth
	if startMonth < 1 || startMonth > 12 {
		startMonth = models.DefaultFiscalYearStartMonth
		d.Settings.FiscalYearStartMonth = startMonth
	}
	year := d.Settings.Year
	if !models.ValidYear(year) {
		year = forecast.FiscalYearOf(now, startMonth)
		d.Settings.Year = year
	}
	fys := make(map[int]models.FiscalYearSettings, len(base.FiscalYears))
	for k, v := range base.FiscalYears {
		fys[k] = v
	}
	d.FiscalYears = fys

	fyStart, fyEnd := forecast.FiscalYear(year, startMonth)
	// A fiscal year the user is not currently living in still has to look
	// populated, so the generator falls back to the closest day inside it.
	ref := clamp(dayOf(now), fyStart, fyEnd)

	d.Projects = buildProjects(year, fyStart, fyEnd, ref)
	models.EnsureVacationProject(&d, year)
	d.Entries = buildEntries(d, year, startMonth, fyStart, fyEnd, ref)
	applyBudgets(d.Projects, d.Entries)
	return d
}

// applyBudgets sets every assignment's budget from the hours actually planned on
// it, so the remaining-budget indicators show the intended over/under picture
// whatever part of the year is currently planned. Both fiscal years of an
// assignment carry the same budget - that is what makes the carry-over visible.
func applyBudgets(ps []models.Project, entries []models.Entry) {
	planned := map[string]float64{}
	for _, e := range entries {
		planned[e.ProjectID] += e.Hours
	}
	budgets := make(map[string]float64, len(projects))
	for _, p := range projects {
		total := planned[idPrefix+p.assignmentID] + p.carryOver
		budget := p.budget
		if total > 0 {
			budget = roundTen(total * p.budgetFactor)
		}
		budgets[p.assignmentID] = budget
	}
	for i := range ps {
		if b, ok := budgets[ps[i].AssignmentID]; ok {
			ps[i].BudgetHours = b
		}
	}
}

// buildProjects creates the fiscal year's project rows plus the previous year's
// counterparts of every continued assignment (same assignment ID = pooled
// hours, which is what the carry-over column shows).
func buildProjects(year int, fyStart, fyEnd, ref time.Time) []models.Project {
	out := make([]models.Project, 0, len(projects)*2)
	for _, p := range projects {
		start, end := window(fyStart, fyEnd, ref, p)
		out = append(out, models.Project{
			ID:           idPrefix + p.assignmentID,
			AssignmentID: p.assignmentID,
			Name:         p.name,
			BudgetHours:  p.budget,
			Color:        p.color,
			Active:       p.active,
			FiscalYear:   year,
			StartDate:    isoOrEmpty(start, p.startMonth < 0),
			EndDate:      isoOrEmpty(end, p.endMonth < 0 && p.active),
		})
	}
	for _, p := range projects {
		if p.carryOver <= 0 {
			continue
		}
		out = append(out, models.Project{
			ID:           idPrefix + "prev-" + p.assignmentID,
			AssignmentID: p.assignmentID,
			Name:         p.name,
			BudgetHours:  p.budget,
			Color:        p.color,
			Active:       false,
			FiscalYear:   year - 1,
		})
	}
	return out
}

// window returns a project's inclusive booking window inside the fiscal year.
// An inactive project additionally ends shortly before the reference day: it is
// gone from the forecast grid, so forecast hours on it would make the grid rows
// and the day totals disagree. The cut never runs past the project's own start,
// which would be an invalid window.
func window(fyStart, fyEnd, ref time.Time, p project) (start, end time.Time) {
	start = fyStart
	if p.startMonth > 0 {
		start = fyStart.AddDate(0, p.startMonth, 0)
	}
	end = fyEnd
	if p.endMonth >= 0 {
		end = fyStart.AddDate(0, p.endMonth+1, 0).AddDate(0, 0, -1)
	}
	if !p.active {
		if stop := ref.AddDate(0, 0, -7); stop.Before(end) {
			end = stop
		}
	}
	if end.After(fyEnd) {
		end = fyEnd
	}
	if end.Before(start) {
		end = start
	}
	return start, end
}

// isoOrEmpty formats a window bound, or returns "" for an unbounded one - an
// empty bound defaults to the fiscal-year start or end.
func isoOrEmpty(t time.Time, unbounded bool) string {
	if unbounded {
		return ""
	}
	return t.Format(dateFmt)
}

// buildEntries books every FY week from the start of the year up to a few weeks
// past the reference day: the week's total from weekHours, spread over its
// workdays and split across the projects whose window covers that day. Public
// holidays and the vacation blocks are excluded.
func buildEntries(d models.Data, year, startMonth int, fyStart, fyEnd, ref time.Time) []models.Entry {
	cal := holidays.Get(year, d.Settings.FederalState)
	vacation := vacationDays(ref, fyStart, fyEnd)
	vacationID := models.VacationProjectID(year)
	fyProjects := models.ProjectsForFY(d.Projects, year)
	shares := make(map[string]int, len(projects))
	for _, p := range projects {
		shares[idPrefix+p.assignmentID] = p.share
	}

	// End on a Friday, otherwise the horizon cuts a week in half and the last
	// Sankey column shows a stub.
	last := horizon(fyStart, fyEnd, ref)

	out := carryOverEntries(fyStart)
	for w := 1; w <= forecast.FYWeeks(year, startMonth); w++ {
		monday := forecast.FYWeekMonday(year, startMonth, w)
		if monday.After(last) {
			break
		}
		workdays := make([]time.Time, 0, 5)
		for i := 0; i < 5; i++ {
			day := monday.AddDate(0, 0, i)
			if day.Before(fyStart) || day.After(last) {
				continue
			}
			iso := day.Format(dateFmt)
			if cal.IsHoliday(iso) {
				continue
			}
			if vacation[iso] {
				out = append(out, models.Entry{Date: iso, ProjectID: vacationID, Hours: vacationDayHours})
				continue
			}
			workdays = append(workdays, day)
		}
		if len(workdays) == 0 {
			continue
		}
		// A week shortened by holidays or vacation is scaled down instead of
		// squeezing the full week onto the remaining days.
		total := roundHalf(weekHours[(w-1)%len(weekHours)] * float64(len(workdays)) / 5)
		for i, day := range workdays {
			hours := dayShare(total, len(workdays), i)
			out = append(out, dayEntries(fyProjects, shares, day.Format(dateFmt), hours, w+i)...)
		}
	}
	return out
}

// horizon is the last day the sample plans hours for: the end of the half-year
// the reference day falls into - a year is planned half by half, not week by
// week - but always at least forecastWeeks ahead, so the forward-looking charts
// never run empty at the end of a half-year. It ends on a Friday, otherwise the
// horizon cuts a week in half and the last Sankey column shows a stub.
func horizon(fyStart, fyEnd, ref time.Time) time.Time {
	months := 6
	if !ref.Before(fyStart.AddDate(0, 6, 0)) {
		months = 12
	}
	end := fyStart.AddDate(0, months, 0).AddDate(0, 0, -1)
	if min := ref.AddDate(0, 0, forecastWeeks*7); end.Before(min) {
		end = min
	}
	end = mondayOf(end).AddDate(0, 0, 4)
	if end.After(fyEnd) {
		end = fyEnd
	}
	return end
}

// dayShare splits a week's hours over its workdays: an even base for every day
// and the rounding remainder on the last one.
func dayShare(total float64, days, i int) float64 {
	base := roundHalf(total / float64(days))
	if i < days-1 {
		return base
	}
	return round1(total - base*float64(days-1))
}

// dayEntries splits one day's hours over the projects whose booking window
// covers that day, weighted by their share. Two projects per day keep the grid
// readable; a third joins every third day. The rotation moves the leading
// project along, so no single band dominates the Sankey.
func dayEntries(ps []models.Project, shares map[string]int, iso string, total float64, rot int) []models.Entry {
	type candidate struct {
		id    string
		share int
	}
	var cands []candidate
	for _, p := range ps {
		if shares[p.ID] <= 0 || !p.Bookable(iso) {
			continue
		}
		cands = append(cands, candidate{id: p.ID, share: shares[p.ID]})
	}
	if len(cands) == 0 || total <= 0 {
		return nil
	}
	lead := rot % len(cands)
	ordered := make([]candidate, 0, len(cands))
	ordered = append(ordered, cands[lead:]...)
	ordered = append(ordered, cands[:lead]...)
	take := 2
	if rot%3 == 0 {
		take = 3
	}
	if take > len(ordered) {
		take = len(ordered)
	}
	ordered = ordered[:take]

	weights := 0
	for _, c := range ordered {
		weights += c.share
	}
	out := make([]models.Entry, 0, len(ordered))
	left := total
	for i, c := range ordered {
		hours := roundHalf(total * float64(c.share) / float64(weights))
		if i == len(ordered)-1 || hours > left {
			hours = round1(left)
		}
		left = round1(left - hours)
		if hours <= 0 {
			continue
		}
		out = append(out, models.Entry{Date: iso, ProjectID: c.id, Hours: hours})
	}
	return out
}

// carryOverEntries books the previous fiscal year of every continued assignment
// in the weeks right before the current fiscal year starts. Hours belong to a
// fiscal year by their date, which is exactly what the carry-over column shows.
func carryOverEntries(fyStart time.Time) []models.Entry {
	var out []models.Entry
	for _, p := range projects {
		if p.carryOver <= 0 {
			continue
		}
		left := p.carryOver
		day := fyStart.AddDate(0, 0, -1)
		for left > 0 {
			if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
				hours := vacationDayHours
				if hours > left {
					hours = left
				}
				out = append(out, models.Entry{
					Date:      day.Format(dateFmt),
					ProjectID: idPrefix + "prev-" + p.assignmentID,
					Hours:     hours,
				})
				left -= hours
			}
			day = day.AddDate(0, 0, -1)
		}
	}
	return out
}

// vacationDays returns the ISO dates covered by the sample vacation: two weeks
// already taken, a bridge day and one week right after the reference day, so
// the vacation band visibly absorbs the other projects and releases them again.
func vacationDays(ref, fyStart, fyEnd time.Time) map[string]bool {
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
			out[day.Format(dateFmt)] = true
		}
	}
	mark(mondayOf(ref.AddDate(0, 0, -84)), 5)
	mark(mondayOf(ref.AddDate(0, 0, -35)), 5)
	mark(mondayOf(ref.AddDate(0, 0, -14)), 1)
	mark(mondayOf(ref.AddDate(0, 0, 7)), 5)
	return out
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func clamp(t, min, max time.Time) time.Time {
	if t.Before(min) {
		return min
	}
	if t.After(max) {
		return max
	}
	return t
}

func mondayOf(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7
	return dayOf(t).AddDate(0, 0, -off)
}

func roundHalf(f float64) float64 {
	return float64(int(f*2+0.5)) / 2
}

// roundTen rounds up to whole tens, with a floor of 10, so a derived budget
// reads like a number a human would have typed.
func roundTen(f float64) float64 {
	if f < 10 {
		return 10
	}
	return float64(int(f/10+0.5)) * 10
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
