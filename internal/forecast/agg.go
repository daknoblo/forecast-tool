package forecast

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// DayCell holds the hours for a single day across all projects. There is a
// single hours value per project; whether it counts as "booked" or "forecast"
// is derived from the day's date (Past marks a booked, i.e. before-today, day).
type DayCell struct {
	Date         string             // YYYY-MM-DD
	WeekdayName  string             // Mo, Di, ...
	InYear       bool               // belongs to the configured fiscal year
	IsHoliday    bool               // public holiday
	HolidayName  string             // holiday label, if any
	HolidayHours float64            // auto-booked hours for a weekday holiday (8h)
	MonthEnd     bool               // the next displayed weekday is in a new month
	Past         bool               // the day lies before today (booked, not forecast)
	Hours        map[string]float64 // projectID -> hours
	Total        float64            // hours sum over projects
}

// WeekView aggregates a single fiscal-year week (Mon-Fri).
type WeekView struct {
	Year           int
	Week           int // 1-based fiscal-year week index
	ISOWeek        int
	Label          string
	RangeLabel     string
	Days           []DayCell
	ProjectTotals  map[string]float64 // projectID -> hours over the week
	Total          float64            // hours sum over all projects (status/utilization basis)
	HolidayHours   float64
	TargetHours    float64
	UtilizationPct float64
	Status         models.UtilStatus // booking traffic-light for this week
	PrevWeek       int
	NextWeek       int
}

var weekdayNames = []string{"Mo", "Di", "Mi", "Do", "Fr"}

// HolidayDayHours is the number of hours a public holiday on a weekday
// automatically contributes towards the fiscal-year goal.
const HolidayDayHours = 8.0

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

// normMonth clamps a fiscal-year start month into 1..12, defaulting to July.
func normMonth(startMonth int) int {
	if startMonth < 1 || startMonth > 12 {
		return 7
	}
	return startMonth
}

// FiscalYear returns the inclusive [start, end] dates of the fiscal year
// labelled by the given year. The fiscal year is named after the calendar year
// in which it ENDS: with startMonth==7, FY 2027 runs 01.07.2026–30.06.2027.
// With startMonth==1 it equals the calendar year (no shift).
func FiscalYear(year, startMonth int) (time.Time, time.Time) {
	startMonth = normMonth(startMonth)
	startYear := year
	if startMonth > 1 {
		// The FY ends in `year`, so it starts in the previous calendar year.
		startYear = year - 1
	}
	start := time.Date(startYear, time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0).AddDate(0, 0, -1)
	return start, end
}

// mondayOf returns the Monday (00:00 UTC) of the week containing t.
func mondayOf(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7 // days since Monday
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -off)
}

// FYWeekMonday returns the Monday of the given 1-based fiscal-year week.
func FYWeekMonday(year, startMonth, week int) time.Time {
	start, _ := FiscalYear(year, startMonth)
	return mondayOf(start).AddDate(0, 0, (week-1)*7)
}

// FYWeeks returns the number of Monday-based weeks spanning the fiscal year.
func FYWeeks(year, startMonth int) int {
	start, end := FiscalYear(year, startMonth)
	first := mondayOf(start)
	days := int(end.Sub(first).Hours()/24) + 1
	return (days + 6) / 7
}

// CurrentFYWeek returns the 1-based fiscal-year week for today, clamped to the FY.
func CurrentFYWeek(year, startMonth int) int {
	now := time.Now().UTC()
	start, _ := FiscalYear(year, startMonth)
	first := mondayOf(start)
	wk := int(mondayOf(now).Sub(first).Hours()/24)/7 + 1
	max := FYWeeks(year, startMonth)
	if wk < 1 {
		wk = 1
	}
	if wk > max {
		wk = max
	}
	return wk
}

// FYWeekIndexOf returns the 1-based fiscal-year week containing t, or 0 if t is
// outside the fiscal year.
func FYWeekIndexOf(year, startMonth int, t time.Time) int {
	start, end := FiscalYear(year, startMonth)
	if t.Before(start) || t.After(end) {
		return 0
	}
	first := mondayOf(start)
	days := int(mondayOf(t).Sub(first).Hours() / 24)
	return days/7 + 1
}

// todayISO returns today's date (UTC) as an ISO YYYY-MM-DD string. Days before
// it are "booked" (past); today and later are "forecast".
func todayISO() string {
	return time.Now().UTC().Format("2006-01-02")
}

// hoursIndex maps "date|projectId" to the summed hours for that day and project.
func hoursIndex(entries []models.Entry) map[string]float64 {
	idx := make(map[string]float64, len(entries))
	for _, e := range entries {
		idx[e.Date+"|"+e.ProjectID] += e.Hours
	}
	return idx
}

// vacationSet returns the set of vacation project IDs. Vacation is a normal,
// editable project: its hours count towards the weekly utilization because they
// consume available working time. They are only excluded from the FY goal (they
// are not billable work) and from the dashboard sankey.
func vacationSet(ps []models.Project) map[string]bool {
	set := map[string]bool{}
	for _, p := range ps {
		if p.IsVacation() {
			set[p.ID] = true
		}
	}
	return set
}

// knownProjects returns the set of existing project IDs. Entries pointing at a
// project that no longer exists are ignored by every roll-up, so the goal, the
// year summary and the grids always count the same hours.
func knownProjects(ps []models.Project) map[string]bool {
	set := make(map[string]bool, len(ps))
	for _, p := range ps {
		set[p.ID] = true
	}
	return set
}

// BuildWeek assembles the Mon-Fri view for one fiscal-year week.
func BuildWeek(d models.Data, cal *holidays.Calendar, week int) WeekView {
	return buildWeek(d, cal, week, hoursIndex(d.Entries), todayISO())
}

// buildWeek is the shared implementation of BuildWeek. It takes the pre-built
// hours index and today's date so a multi-week span does not have to rebuild
// them for every single week.
func buildWeek(d models.Data, cal *holidays.Calendar, week int, hidx map[string]float64, today string) WeekView {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	monday := FYWeekMonday(year, startMonth, week)
	fyStart, fyEnd := FiscalYear(year, startMonth)

	_, isoWeek := monday.ISOWeek()
	friday := monday.AddDate(0, 0, 4)
	wv := WeekView{
		Year:          year,
		Week:          week,
		ISOWeek:       isoWeek,
		Label:         fmt.Sprintf("Woche %d · KW %02d", week, isoWeek),
		RangeLabel:    monday.Format("02.01.") + "–" + friday.Format("02.01.2006"),
		ProjectTotals: map[string]float64{},
		TargetHours:   d.Settings.WeeklyTargetHours,
		PrevWeek:      week - 1,
		NextWeek:      week + 1,
	}

	for i := 0; i < 5; i++ {
		day := monday.AddDate(0, 0, i)
		iso := day.Format("2006-01-02")
		inYear := !day.Before(fyStart) && !day.After(fyEnd)
		// Mark a month boundary on the right edge of the last weekday of a month.
		// On Friday, look ahead to Monday so a month ending on the weekend is
		// still drawn after Friday.
		next := day.AddDate(0, 0, 1)
		if i == 4 {
			next = day.AddDate(0, 0, 3)
		}
		cell := DayCell{
			Date:        iso,
			WeekdayName: weekdayNames[i],
			InYear:      inYear,
			IsHoliday:   cal.IsHoliday(iso),
			HolidayName: cal.Name(iso),
			MonthEnd:    next.Month() != day.Month(),
			Past:        iso < today,
			Hours:       map[string]float64{},
		}
		if cell.IsHoliday {
			cell.HolidayHours = HolidayDayHours
			if inYear {
				wv.HolidayHours += HolidayDayHours
			}
		}
		// Days outside the fiscal year stay visible and editable, but their hours
		// belong to the neighbouring FY: counting them here would book the old
		// year's hours onto this one.
		for _, p := range d.Projects {
			h := hidx[iso+"|"+p.ID]
			if h == 0 {
				continue
			}
			cell.Hours[p.ID] = h
			cell.Total += h
			if inYear {
				wv.ProjectTotals[p.ID] += h
			}
		}
		if inYear {
			wv.Total += cell.Total
		}
		wv.Days = append(wv.Days, cell)
	}

	wv.Total = round1(wv.Total)
	wv.Status = d.Settings.ClassifyUtilization(wv.Total)
	if wv.TargetHours > 0 {
		wv.UtilizationPct = round1(wv.Total / wv.TargetHours * 100)
	}
	return wv
}

// formatDayDot turns an ISO date (YYYY-MM-DD) into German DD.MM.YYYY.
func formatDayDot(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("02.01.2006")
}

// shortWeekdays are the abbreviated German weekday names, indexed by
// time.Weekday (Sunday = 0).
var shortWeekdays = []string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// formatDayWithWeekday renders a date with its short weekday, e.g.
// "Mo. 01.07.2027".
func formatDayWithWeekday(t time.Time) string {
	return shortWeekdays[int(t.Weekday())] + ". " + t.Format("02.01.2006")
}

// SpanView aggregates several consecutive fiscal-year weeks into one Mon-Fri
// grid so wide screens can show as many days as fit at once.
type SpanView struct {
	StartWeek      int
	EndWeek        int
	Weeks          int
	MaxWeek        int
	PrevStart      int
	NextStart      int
	RangeLabel     string
	Blocks         []WeekView         // one entry per visible week (for header grouping)
	Days           []DayCell          // all days flattened across the visible weeks
	ProjectTotals  map[string]float64 // projectID -> hours over the span
	Total          float64
	HolidayHours   float64
	TargetHours    float64 // weekly target * number of visible weeks
	UtilizationPct float64
}

// BuildSpan assembles a Mon-Fri grid spanning `weeks` consecutive fiscal-year
// weeks starting at startWeek. The start is clamped so the span stays within the
// fiscal year where possible.
func BuildSpan(d models.Data, cal *holidays.Calendar, startWeek, weeks int) SpanView {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	max := FYWeeks(year, startMonth)
	if weeks < 1 {
		weeks = 1
	}
	if weeks > max {
		weeks = max
	}
	if startWeek < 1 {
		startWeek = 1
	}
	if startWeek > max {
		startWeek = max
	}
	if startWeek+weeks-1 > max {
		startWeek = max - weeks + 1
		if startWeek < 1 {
			startWeek = 1
		}
	}

	sv := SpanView{
		StartWeek:     startWeek,
		EndWeek:       startWeek + weeks - 1,
		Weeks:         weeks,
		MaxWeek:       max,
		ProjectTotals: map[string]float64{},
	}
	// Build the hours index once for the whole span instead of once per week.
	hidx := hoursIndex(d.Entries)
	today := todayISO()
	for i := 0; i < weeks; i++ {
		wv := buildWeek(d, cal, startWeek+i, hidx, today)
		sv.Blocks = append(sv.Blocks, wv)
		sv.Days = append(sv.Days, wv.Days...)
		for pid, h := range wv.ProjectTotals {
			sv.ProjectTotals[pid] += h
		}
		sv.Total += wv.Total
		sv.HolidayHours += wv.HolidayHours
	}
	sv.Total = round1(sv.Total)
	sv.HolidayHours = round1(sv.HolidayHours)
	sv.TargetHours = round1(d.Settings.WeeklyTargetHours * float64(weeks))
	if sv.TargetHours > 0 {
		sv.UtilizationPct = round1(sv.Total / sv.TargetHours * 100)
	}
	sv.PrevStart = startWeek - weeks
	if sv.PrevStart < 1 {
		sv.PrevStart = 1
	}
	sv.NextStart = startWeek + weeks
	if len(sv.Days) > 0 {
		sv.RangeLabel = formatDayDot(sv.Days[0].Date) + "–" + formatDayDot(sv.Days[len(sv.Days)-1].Date)
	}
	return sv
}

// SpanBurn is the combined burn rate of the projects whose booking window
// overlaps a visible date span (used above the forecast grid).
type SpanBurn struct {
	PerWeek    float64
	PerWorkday float64
	Items      []SpanBurnItem
}

// SpanBurnItem is one project's burn rate contributing to a SpanBurn.
type SpanBurnItem struct {
	Name       string
	Color      string
	PerWeek    float64
	PerWorkday float64
}

// BuildSpanBurn sums the per-project burn rates (from a year summary) of the
// active projects whose booking window overlaps the inclusive [spanStart,
// spanEnd] date range (ISO YYYY-MM-DD). ISO strings compare lexicographically.
// Vacation is left out: it is time off, not billable work, so it must not raise
// the rate the grid is measured against.
func BuildSpanBurn(ps []ProjectSummary, spanStart, spanEnd string) SpanBurn {
	var sb SpanBurn
	for _, p := range ps {
		if !p.Project.Active || p.Project.IsVacation() {
			continue
		}
		// no overlap if the window ends before the span or starts after it
		if p.EndDate < spanStart || p.StartDate > spanEnd {
			continue
		}
		sb.Items = append(sb.Items, SpanBurnItem{
			Name:       p.Project.Name,
			Color:      p.Project.Color,
			PerWeek:    p.BurnPerWeek,
			PerWorkday: p.BurnPerWorkday,
		})
		sb.PerWeek += p.BurnPerWeek
		sb.PerWorkday += p.BurnPerWorkday
	}
	sb.PerWeek = round1(sb.PerWeek)
	sb.PerWorkday = round1(sb.PerWorkday)
	return sb
}

// FYHours is the share of an assignment's hours that falls into one fiscal
// year. The attribution follows the entry DATES, so an assignment running past
// the fiscal-year boundary is split exactly at that boundary.
type FYHours struct {
	Year    int     // fiscal-year anchor
	Label   string  // "FY 2027"
	Hours   float64 // hours dated inside that fiscal year
	Current bool    // the fiscal year under review
	Past    bool    // an earlier fiscal year
}

// ProjectSummary describes budget consumption for one project.
type ProjectSummary struct {
	Project  models.Project
	Forecast float64 // hours on today/future days inside this fiscal year
	Actual   float64 // hours on past days inside this fiscal year (booked)
	Consumed float64 // all hours dated INSIDE this fiscal year (forecast + booked)

	// An assignment can run across several fiscal years. Hours belong to the
	// fiscal year their date falls into, so with a July start everything up to
	// 30 June stays in the old FY and everything from 1 July counts towards the
	// new one. Because the project is re-created per fiscal year while it keeps
	// carrying the assignment's total budget, the earlier years' hours have to be
	// deducted, or they would silently be granted again.
	//
	// An INACTIVE project is finished: everything booked or forecast on it stays
	// untouched, but the budget that was never planned is assumed to never be
	// called off again. It is released and drops out of the available budget, the
	// remainder and the burn rate.
	CarryOver       float64   // hours of this assignment dated in earlier fiscal years
	FutureFY        float64   // hours of this assignment dated in later fiscal years
	FYSplit         []FYHours // hours per fiscal year, ascending (only years with hours)
	SpansFY         bool      // the assignment has hours in more than one fiscal year
	Released        float64   // unplanned budget given up because the project is inactive
	AvailableBudget float64   // BudgetHours - CarryOver - Released (what is left for this FY)

	Remaining      float64 // AvailableBudget - Consumed
	UtilizationPct float64 // (CarryOver + Consumed) / budget * 100, across all fiscal years
	CarryOverPct   float64 // CarryOver / budget * 100, the share spent in earlier years
	// The two shares below measure this fiscal year and therefore run against
	// AvailableBudget: 100 % planned means the whole remaining budget is spoken
	// for, whether it is already booked or still forecast.
	PlannedPct float64 // Consumed (booked + forecast) / AvailableBudget * 100
	ActualPct  float64 // booked / AvailableBudget * 100

	// Booking window (clamped to the fiscal year). Empty project dates default
	// to the FY bounds.
	StartDate       string // effective window start, ISO YYYY-MM-DD
	EndDate         string // effective window end, ISO YYYY-MM-DD
	StartLabel      string // DD.MM.YYYY
	EndLabel        string // DD.MM.YYYY
	HasCustomWindow bool   // true if the project sets an explicit start or end
	RemainingLabel  string // time left until the window end, e.g. "noch 2 Wochen und 3 Tage"

	// Burn-rate over the window (holiday-aware working days, Mon-Fri). It is
	// based on AvailableBudget, so an assignment continued from an earlier
	// fiscal year does not get its already-burned hours back.
	WindowWorkdays     int     // working days within the window
	BurnPerWeek        float64 // available budget spread evenly per week of the window
	BurnPerWorkday     float64 // available budget spread evenly per working day
	RemainingWorkdays  int     // working days from today until the window end
	RequiredPerWorkday float64 // remaining budget / remaining working days
	RequiredPerWeek    float64 // RequiredPerWorkday * 5, for comparison with BurnPerWeek
	OutOfWindow        float64 // effective hours booked outside the window (warning)
}

// YearSummary aggregates all projects and weekly totals for the fiscal year.
//
// The Total* roll-ups describe the ASSIGNMENT work of the fiscal year and
// therefore leave the vacation project out: its budget is derived from the
// configured vacation days and its hours never count towards the goal, so
// mixing it in would inflate the budget and make TotalRemaining meaningless.
// Projects still contains the vacation project as its own row.
type YearSummary struct {
	Projects       []ProjectSummary
	TotalHours     float64 // booked + forecast hours dated inside the fiscal year
	TotalBudget    float64 // summed budget of the assignments
	TotalCarryOver float64 // hours of those assignments already spent in earlier fiscal years
	TotalReleased  float64 // unplanned budget given up by inactive assignments
	TotalAvailable float64 // TotalBudget - TotalCarryOver - TotalReleased (usable in this fiscal year)
	TotalRemaining float64 // TotalAvailable - TotalHours (neither booked nor planned yet)
	TotalForecast  float64 // summed forecast hours (today and later)
	TotalActual    float64 // summed booked hours (past days)
	HasCarryOver   bool    // true when at least one project carries hours over
	HasReleased    bool    // true when at least one inactive assignment released budget
	HasFYSplit     bool    // true when at least one assignment spans fiscal years
	WeekTotals     []WeekTotal
	// LastPlannedWeek is the highest fiscal-year week that has hours (0 when the
	// year is empty). The weekly tables list every week up to it — including the
	// ones without hours, so gaps in the plan are visible — and stop there,
	// because beyond the last forecast there is nothing to judge yet.
	LastPlannedWeek int
}

// WeekTotal is the summed hours for a single fiscal-year week.
type WeekTotal struct {
	Week           int // fiscal-year week index
	ISOWeek        int
	Label          string
	RangeLabel     string  // Mon-Fri range, e.g. "Mo. 01.07.2027 – Fr. 05.07.2027"
	Hours          float64 // all hours in this week (booked + forecast)
	Forecast       float64 // hours on today/future days in this week
	Actual         float64 // hours on past days in this week (booked)
	TargetHours    float64
	UtilizationPct float64
	Status         models.UtilStatus // booking traffic-light for this week
}

// projectWindow returns the inclusive [start, end] booking window of a project,
// clamped to the fiscal year. Empty project dates default to the FY bounds.
func projectWindow(p models.Project, fyStart, fyEnd time.Time) (time.Time, time.Time) {
	start, end := fyStart, fyEnd
	if p.StartDate != "" {
		if t, err := time.Parse("2006-01-02", p.StartDate); err == nil && t.After(start) {
			start = t
		}
	}
	if p.EndDate != "" {
		if t, err := time.Parse("2006-01-02", p.EndDate); err == nil && t.Before(end) {
			end = t
		}
	}
	return start, end
}

// countWorkdays returns the number of working days (Mon-Fri, excluding public
// holidays) in the inclusive range [start, end]. Returns 0 if start is after end.
func countWorkdays(start, end time.Time, cal *holidays.Calendar) int {
	n := 0
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		wd := day.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if cal.IsHoliday(day.Format("2006-01-02")) {
			continue
		}
		n++
	}
	return n
}

// remainingLabel renders the calendar time left until the inclusive end date in
// a compact German form, e.g. "noch 7 Tage", "noch 2 Wochen und 3 Tage" or
// "abgelaufen". The label is self-contained so callers never prefix it.
func remainingLabel(from, to time.Time) string {
	days := int(to.Sub(from).Hours()/24) + 1
	switch {
	case days <= 0:
		return "abgelaufen"
	case days == 1:
		return "noch 1 Tag"
	case days < 7:
		return fmt.Sprintf("noch %d Tage", days)
	case days < 60:
		weeks, rest := days/7, days%7
		out := fmt.Sprintf("noch %d Wochen", weeks)
		if weeks == 1 {
			out = "noch 1 Woche"
		}
		switch {
		case rest == 1:
			out += " und 1 Tag"
		case rest > 1:
			out += fmt.Sprintf(" und %d Tage", rest)
		}
		return out
	default:
		return fmt.Sprintf("noch %d Monate", (days+15)/30)
	}
}

// assignmentKey normalizes a project's assignment ID so the same assignment is
// recognised across fiscal years. Projects without an assignment ID (e.g. the
// vacation project) return "" and are never grouped.
func assignmentKey(p models.Project) string {
	return strings.ToLower(strings.TrimSpace(p.AssignmentID))
}

// groupKey returns the key under which a project's hours are pooled across
// fiscal years: the assignment ID when set, otherwise the project's own ID (the
// vacation project has no assignment and therefore stands on its own).
func groupKey(p models.Project) string {
	if k := assignmentKey(p); k != "" {
		return "a:" + k
	}
	return "p:" + p.ID
}

// FiscalYearOf returns the fiscal-year anchor a date belongs to. Hours always
// count towards the fiscal year their DATE falls into, regardless of which
// project row they were booked on: an assignment running past the fiscal-year
// boundary therefore splits itself automatically (with start month July,
// everything up to 30 June stays in the old FY, everything from 1 July counts
// towards the new one). It is the inverse of FiscalYear.
func FiscalYearOf(t time.Time, startMonth int) int {
	startMonth = normMonth(startMonth)
	if startMonth == 1 {
		return t.Year() // start month January == calendar year
	}
	if int(t.Month()) >= startMonth {
		return t.Year() + 1
	}
	return t.Year()
}

// BuildYearSummary computes per-project consumption and weekly totals over the
// fiscal year. There is a single hours value per day and project; it counts as
// booked when the day is in the past and as forecast for today and future days.
//
// Hours are attributed to a fiscal year by their date and pooled per assignment,
// so an assignment that runs across the fiscal-year boundary splits exactly at
// that boundary. d.Projects may (and should) contain the projects of ALL fiscal
// years: the summary itself is restricted to d.Settings.Year, while the other
// years are needed for the carry-over and the per-year split.
func BuildYearSummary(d models.Data, cal *holidays.Calendar) YearSummary {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	fyStart, fyEnd := FiscalYear(year, startMonth)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	todayStr := today.Format("2006-01-02")
	hidx := hoursIndex(d.Entries)

	projByID := map[string]models.Project{}
	for _, p := range d.Projects {
		projByID[p.ID] = p
	}

	groupYear := map[string]map[int]float64{} // group -> fiscal year -> hours
	forecastByGroup := map[string]float64{}   // reviewed fiscal year only
	actualByGroup := map[string]float64{}     // reviewed fiscal year only
	outByP := map[string]float64{}
	weekSum := map[int]float64{}
	weekForecast := map[int]float64{}
	weekActual := map[int]float64{}

	for k, v := range hidx {
		sep := strings.IndexByte(k, '|')
		dateStr, pid := k[:sep], k[sep+1:]
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		past := dateStr < todayStr

		p, ok := projByID[pid]
		if !ok {
			continue // the project was deleted; the grids ignore those hours too
		}
		g := groupKey(p)
		byYear := groupYear[g]
		if byYear == nil {
			byYear = map[int]float64{}
			groupYear[g] = byYear
		}
		entryFY := FiscalYearOf(t, startMonth)
		byYear[entryFY] += v
		if entryFY == year {
			if past {
				actualByGroup[g] += v // past days are booked
			} else {
				forecastByGroup[g] += v // today and future are forecast
			}
			if !p.Bookable(dateStr) {
				outByP[pid] += v
			}
		}

		// Weekly totals over the reviewed fiscal year.
		if w := FYWeekIndexOf(year, startMonth, t); w >= 1 {
			weekSum[w] += v
			if past {
				weekActual[w] += v
			} else {
				weekForecast[w] += v
			}
		}
	}

	ys := YearSummary{}
	// Hours are pooled per assignment, so every row of the same assignment
	// reports the same figures. A document that (invalidly) carries several rows
	// for one assignment in the same fiscal year must therefore still contribute
	// those hours to the roll-ups exactly once.
	rolledUp := map[string]bool{}
	for _, p := range models.ProjectsForFY(d.Projects, year) {
		g := groupKey(p)
		byYear := groupYear[g]
		c := byYear[year]

		// Split the assignment's hours across the fiscal years they fall into.
		years := make([]int, 0, len(byYear))
		for y := range byYear {
			years = append(years, y)
		}
		sort.Ints(years)
		var over, future float64
		split := make([]FYHours, 0, len(years))
		for _, y := range years {
			h := round1(byYear[y])
			if h == 0 {
				continue
			}
			switch {
			case y < year:
				over += byYear[y]
			case y > year:
				future += byYear[y]
			}
			split = append(split, FYHours{
				Year:    y,
				Label:   fmt.Sprintf("FY %d", y),
				Hours:   h,
				Current: y == year,
				Past:    y < year,
			})
		}
		if over > p.BudgetHours {
			over = p.BudgetHours // never show a negative available budget
		}
		avail := p.BudgetHours - over
		// An inactive project is done: booked and forecast hours stay, the rest of
		// its budget is released because it will not be called off any more.
		released := 0.0
		if !p.Active && avail > c {
			released = avail - c
			avail = c
		}
		rem := avail - c
		// An exhausted budget reads as 100 %, not as a division by zero.
		pctOf := func(v, base float64) float64 {
			switch {
			case base > 0:
				return round1(v / base * 100)
			case v > 0:
				return 100
			default:
				return 0
			}
		}
		util := 0.0
		if p.BudgetHours > 0 {
			util = round1((over + c) / p.BudgetHours * 100)
		}
		coPct := 0.0
		if p.BudgetHours > 0 {
			coPct = round1(over / p.BudgetHours * 100)
		}
		plannedPct := pctOf(c, avail)
		aPct := pctOf(actualByGroup[g], avail)

		wStart, wEnd := projectWindow(p, fyStart, fyEnd)
		workdays := countWorkdays(wStart, wEnd, cal)
		remStart := wStart
		if today.After(remStart) {
			remStart = today
		}
		remWorkdays := countWorkdays(remStart, wEnd, cal)
		burnPerWorkday := 0.0
		burnPerWeek := 0.0
		if workdays > 0 {
			burnPerWorkday = round1(avail / float64(workdays))
			burnPerWeek = round1(avail / (float64(workdays) / 5.0))
		}
		requiredPerWorkday := 0.0
		if remWorkdays > 0 && rem > 0 {
			requiredPerWorkday = round1(rem / float64(remWorkdays))
		}

		ys.Projects = append(ys.Projects, ProjectSummary{
			Project:            p,
			Forecast:           round1(forecastByGroup[g]),
			Actual:             round1(actualByGroup[g]),
			Consumed:           round1(c),
			CarryOver:          round1(over),
			FutureFY:           round1(future),
			FYSplit:            split,
			SpansFY:            len(split) > 1,
			Released:           round1(released),
			AvailableBudget:    round1(avail),
			Remaining:          round1(rem),
			UtilizationPct:     util,
			CarryOverPct:       coPct,
			PlannedPct:         plannedPct,
			ActualPct:          aPct,
			StartDate:          wStart.Format("2006-01-02"),
			EndDate:            wEnd.Format("2006-01-02"),
			StartLabel:         wStart.Format("02.01.2006"),
			EndLabel:           wEnd.Format("02.01.2006"),
			HasCustomWindow:    p.StartDate != "" || p.EndDate != "",
			RemainingLabel:     remainingLabel(today, wEnd),
			WindowWorkdays:     workdays,
			BurnPerWeek:        burnPerWeek,
			BurnPerWorkday:     burnPerWorkday,
			RemainingWorkdays:  remWorkdays,
			RequiredPerWorkday: requiredPerWorkday,
			RequiredPerWeek:    round1(requiredPerWorkday * 5),
			OutOfWindow:        round1(outByP[p.ID]),
		})
		// The roll-ups describe the assignment work of the fiscal year; the
		// vacation project has its own derived budget and never counts towards
		// the goal, so it stays out of them.
		if !p.IsVacation() && !rolledUp[g] {
			rolledUp[g] = true
			ys.TotalHours += c
			ys.TotalBudget += p.BudgetHours
			ys.TotalCarryOver += over
			ys.TotalReleased += released
			ys.TotalForecast += forecastByGroup[g]
			ys.TotalActual += actualByGroup[g]
			if over > 0 {
				ys.HasCarryOver = true
			}
			if released > 0 {
				ys.HasReleased = true
			}
		}
		if len(split) > 1 {
			ys.HasFYSplit = true
		}
	}
	ys.TotalHours = round1(ys.TotalHours)
	ys.TotalBudget = round1(ys.TotalBudget)
	ys.TotalCarryOver = round1(ys.TotalCarryOver)
	ys.TotalReleased = round1(ys.TotalReleased)
	ys.TotalAvailable = round1(ys.TotalBudget - ys.TotalCarryOver - ys.TotalReleased)
	ys.TotalRemaining = round1(ys.TotalAvailable - ys.TotalHours)
	ys.TotalForecast = round1(ys.TotalForecast)
	ys.TotalActual = round1(ys.TotalActual)

	weeks := FYWeeks(year, startMonth)
	for w := 1; w <= weeks; w++ {
		util := 0.0
		if d.Settings.WeeklyTargetHours > 0 {
			util = round1(weekSum[w] / d.Settings.WeeklyTargetHours * 100)
		}
		monday := FYWeekMonday(year, startMonth, w)
		_, isoWeek := monday.ISOWeek()
		hrs := round1(weekSum[w])
		ys.WeekTotals = append(ys.WeekTotals, WeekTotal{
			Week:           w,
			ISOWeek:        isoWeek,
			Label:          fmt.Sprintf("W%d · KW%02d", w, isoWeek),
			RangeLabel:     formatDayWithWeekday(monday) + " – " + formatDayWithWeekday(monday.AddDate(0, 0, 4)),
			Hours:          hrs,
			Forecast:       round1(weekForecast[w]),
			Actual:         round1(weekActual[w]),
			TargetHours:    d.Settings.WeeklyTargetHours,
			UtilizationPct: util,
			Status:         d.Settings.ClassifyUtilization(hrs),
		})
		if hrs > 0 {
			ys.LastPlannedWeek = w
		}
	}
	return ys
}

// WeekToDate reports the utilization reached since the fiscal year started, up
// to the current week. The FY goal is spread evenly over the fiscal year's
// weeks, which yields an average weekly burn rate (e.g. 1440 h / 52 weeks =
// 27.7 h). Booking 40 h in such a week means 12.3 h above plan and pushes the
// rate above 100 %.
//
// Only days that are already over count — today is still forecast and would
// drag the rate down while it is running. Weekends are not part of the elapsed
// weeks; public holidays are, because the annual goal does not shrink because of
// them. Vacation hours never count towards the FY goal and are excluded, exactly
// as on the goal page.
type WeekToDate struct {
	HasData bool // today lies inside the fiscal year and the FY has a goal

	Week       int    // current fiscal-year week index
	ISOWeek    int    // ISO calendar week
	StartLabel string // fiscal-year start, DD.MM.YYYY
	ToLabel    string // last counted day (yesterday), DD.MM.YYYY

	ElapsedWeeks  float64 // elapsed weekdays since the FY start / 5
	FYWeeks       int     // weeks in the whole fiscal year
	Hours         float64 // hours booked since the FY start (vacation excluded)
	ExpectedHours float64 // TargetPerWeek * ElapsedWeeks
	TargetPerWeek float64 // FY goal / FYWeeks (the evenly spread plan)
	PerWeek       float64 // Hours / ElapsedWeeks (the rate actually achieved)
	DeltaPerWeek  float64 // PerWeek - TargetPerWeek (hours per week above/below plan)
	RatePct       float64 // PerWeek / TargetPerWeek * 100 (100 = exactly on plan)
}

// BuildWeekToDate computes the utilization achieved since the fiscal year
// started. It returns HasData == false when the reviewed fiscal year does not
// contain today, when it has no goal, or before the first weekday is over; the
// caller then shows a placeholder.
func BuildWeekToDate(d models.Data, cal *holidays.Calendar) WeekToDate {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	now := time.Now().UTC().Truncate(24 * time.Hour)
	fyStart, fyEnd := FiscalYear(year, startMonth)
	if now.Before(fyStart) || now.After(fyEnd) {
		return WeekToDate{} // another fiscal year is under review: nothing to date
	}

	weeks := FYWeeks(year, startMonth)
	if weeks < 1 {
		weeks = 1
	}
	week := CurrentFYWeek(year, startMonth)
	_, isoWeek := FYWeekMonday(year, startMonth, week).ISOWeek()
	target := BuildFYCapacity(d, cal, year).RemainingHours

	wtd := WeekToDate{
		Week:          week,
		ISOWeek:       isoWeek,
		FYWeeks:       weeks,
		StartLabel:    fyStart.Format("02.01.2006"),
		ToLabel:       now.AddDate(0, 0, -1).Format("02.01.2006"),
		TargetPerWeek: round1(target / float64(weeks)),
	}

	// Count the weekdays that are already over. Holidays are deliberately
	// included: the annual goal stays the same, so a holiday really does put you
	// behind the evenly spread plan.
	weekdays := 0
	for day := fyStart; day.Before(now); day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday {
			weekdays++
		}
	}
	if weekdays == 0 || target <= 0 {
		return wtd
	}

	// Hours booked between the fiscal-year start and yesterday.
	fromISO := fyStart.Format("2006-01-02")
	todayISO := now.Format("2006-01-02")
	vac := vacationSet(d.Projects)
	known := knownProjects(d.Projects)
	for _, e := range d.Entries {
		if e.Date < fromISO || e.Date >= todayISO || vac[e.ProjectID] || !known[e.ProjectID] {
			continue
		}
		wtd.Hours += e.Hours
	}

	elapsedWeeks := float64(weekdays) / 5
	wtd.HasData = true
	wtd.Hours = round1(wtd.Hours)
	wtd.ElapsedWeeks = round1(elapsedWeeks)
	wtd.ExpectedHours = round1(wtd.TargetPerWeek * elapsedWeeks)
	wtd.PerWeek = round1(wtd.Hours / elapsedWeeks)
	wtd.DeltaPerWeek = round1(wtd.PerWeek - wtd.TargetPerWeek)
	if wtd.TargetPerWeek > 0 {
		wtd.RatePct = round1(wtd.PerWeek / wtd.TargetPerWeek * 100)
	}
	return wtd
}

// Working-time limits of the German Arbeitszeitgesetz (§3 ArbZG). The law
// measures working time per *Werktag* - Monday to Saturday, Sunday excluded -
// so a regular 40 h week already sits well below the limit. A single Werktag may
// go up to LongDayHours, but the average over the balancing period must stay at
// or below WorkdayLimitHours.
const (
	WorkdayLimitHours = 8.0  // average per Werktag over the balancing period
	LongDayHours      = 10.0 // hard cap for a single Werktag
)

// WorkloadWindows are the rolling windows (in calendar months) the goal page
// charts, longest first - once looking back over the booked hours and once
// forward over the plan. The 6-month window is the balancing period §3 ArbZG
// names, so it is the one the dashboard tile shows.
var WorkloadWindows = []int{12, 6, 3, 1}

// WorkloadTileMonths is the window behind the dashboard tile.
const WorkloadTileMonths = 6

// Workload is the average working time per Werktag over a rolling window. It
// looks either back from yesterday over the hours already booked - today is
// still running and would only dilute the average - or forward from today over
// the hours planned.
//
// Vacation is not working time: its hours are left out, and a day that carries
// nothing but vacation drops out of the Werktage as well. Counting it as an
// empty Werktag would quietly understate the load of the days actually worked.
type Workload struct {
	Months     int    // window length in calendar months
	Label      string // "6 Monate"
	Ahead      bool   // the window looks forward: planned instead of booked hours
	HasData    bool
	StartLabel string // DD.MM.YYYY
	EndLabel   string // DD.MM.YYYY

	Hours    float64 // hours in the window, vacation excluded
	Days     int     // Werktage (Mon-Sat) in the window, holidays and vacation excluded
	Filled   int     // Werktage that actually carry hours
	PerDay   float64 // Hours / Days - the figure §3 ArbZG caps at 8 h
	MaxHours float64 // Days * WorkdayLimitHours: what the window legally allows
	Headroom float64 // MaxHours - Hours (negative = over the limit)
	PctLimit float64 // PerDay / WorkdayLimitHours * 100
	Over     bool    // the average exceeds the limit

	LongDays  int     // Werktage above LongDayHours
	PeakHours float64 // hours on the busiest single day
	PeakLabel string  // date of that day, DD.MM.YYYY
}

// BuildWorkload measures the average working time per Werktag over the last
// `months` calendar months. The window is anchored on today and can therefore
// span several fiscal years, so every entry counts, not just the reviewed year's.
func BuildWorkload(d models.Data, months int) Workload {
	return buildWorkload(d, months, time.Now().UTC().Truncate(24*time.Hour), false)
}

// BuildWorkloadPlan is BuildWorkload over the NEXT `months` months: it measures
// the forecast instead of the booked hours, so the plan can be checked against
// the same limits before the time is worked.
func BuildWorkloadPlan(d models.Data, months int) Workload {
	return buildWorkload(d, months, time.Now().UTC().Truncate(24*time.Hour), true)
}

// BuildWorkloadSeries measures every window of WorkloadWindows at once.
func BuildWorkloadSeries(d models.Data) []Workload {
	return workloadSeries(d, false)
}

// BuildWorkloadPlanSeries is BuildWorkloadSeries looking forward.
func BuildWorkloadPlanSeries(d models.Data) []Workload {
	return workloadSeries(d, true)
}

func workloadSeries(d models.Data, ahead bool) []Workload {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]Workload, 0, len(WorkloadWindows))
	for _, m := range WorkloadWindows {
		out = append(out, buildWorkload(d, m, now, ahead))
	}
	return out
}

func buildWorkload(d models.Data, months int, now time.Time, ahead bool) Workload {
	w := Workload{Months: months, Label: monthsLabel(months), Ahead: ahead}
	if months < 1 {
		return w
	}
	// Hours from today on are forecast, so a forward window starts today while a
	// backward one stops before it.
	start, end := now.AddDate(0, -months, 0), now.AddDate(0, 0, -1)
	if ahead {
		start, end = now, now.AddDate(0, months, 0).AddDate(0, 0, -1)
	}
	if end.Before(start) {
		return w
	}
	w.StartLabel = start.Format("02.01.2006")
	w.EndLabel = end.Format("02.01.2006")

	// The window is anchored on today, not on the reviewed fiscal year, so it
	// needs its own calendar: the one built for a future fiscal year does not
	// reach far enough back for a 12-month look-back.
	cal := holidays.Get(now.Year(), d.Settings.FederalState)

	vacationProjects := vacationSet(d.Projects)
	known := knownProjects(d.Projects)
	fromISO, toISO := start.Format("2006-01-02"), end.Format("2006-01-02")
	work := map[string]float64{}
	vacation := map[string]bool{}
	for _, e := range d.Entries {
		if e.Date < fromISO || e.Date > toISO || !known[e.ProjectID] || e.Hours <= 0 {
			continue
		}
		if vacationProjects[e.ProjectID] {
			vacation[e.Date] = true
			continue
		}
		work[e.Date] += e.Hours
	}

	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		iso := day.Format("2006-01-02")
		hours := work[iso]
		w.Hours += hours
		if hours > w.PeakHours {
			w.PeakHours, w.PeakLabel = hours, day.Format("02.01.2006")
		}
		if hours > LongDayHours {
			w.LongDays++
		}
		if day.Weekday() == time.Sunday || cal.IsHoliday(iso) {
			continue
		}
		if vacation[iso] && hours == 0 {
			continue
		}
		w.Days++
		if hours > 0 {
			w.Filled++
		}
	}
	if w.Days == 0 || w.Hours <= 0 {
		return w
	}

	w.HasData = true
	w.Hours = round1(w.Hours)
	w.PeakHours = round1(w.PeakHours)
	w.PerDay = round1(w.Hours / float64(w.Days))
	w.MaxHours = round1(float64(w.Days) * WorkdayLimitHours)
	w.Headroom = round1(w.MaxHours - w.Hours)
	w.PctLimit = round1(w.PerDay / WorkdayLimitHours * 100)
	w.Over = w.PerDay > WorkdayLimitHours
	return w
}

func monthsLabel(months int) string {
	if months == 1 {
		return "1 Monat"
	}
	return fmt.Sprintf("%d Monate", months)
}

// BurnPoint is a single data point of a project burn-down curve.
type BurnPoint struct {
	ISOWeek   int // ISO calendar week of the point's Monday
	Month     int // 1-12, month of the point's Monday (for axis labels)
	Year      int // calendar year of the point's Monday
	Remaining float64
}

// BuildBurndown returns the remaining-budget curve for one project over its
// effective booking window, padded by one month before the start and one month
// after the end. Only hours dated inside the window count: the window is
// clamped to the fiscal year, and hours outside it belong to another fiscal
// year (they are already deducted from the budget as a carry-over). Hours are
// pooled per assignment, so a continued assignment whose hours were booked on
// the previous year's row still burns down the same budget the table shows.
func BuildBurndown(d models.Data, projectID, startISO, endISO string, budget float64) []BurnPoint {
	start, errS := time.Parse("2006-01-02", startISO)
	end, errE := time.Parse("2006-01-02", endISO)
	if errS != nil || errE != nil || end.Before(start) {
		// Fallback: span the whole fiscal year.
		start, end = FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	}
	fromISO := start.Format("2006-01-02")
	toISO := end.Format("2006-01-02")

	// Every project row of the same assignment feeds this curve.
	group := ""
	for _, p := range d.Projects {
		if p.ID == projectID {
			group = groupKey(p)
			break
		}
	}
	inGroup := map[string]bool{projectID: true}
	if group != "" {
		for _, p := range d.Projects {
			if groupKey(p) == group {
				inGroup[p.ID] = true
			}
		}
	}

	// Pad the window by one month on each side and align to whole weeks.
	winStart := mondayOf(start.AddDate(0, -1, 0))
	winEnd := end.AddDate(0, 1, 0)

	weeks := int(winEnd.Sub(winStart).Hours()/24)/7 + 1
	if weeks < 1 {
		weeks = 1
	}

	// Bucket the project's hours per week in a single pass over the entries.
	// Each date is parsed exactly once, and its week index is derived
	// arithmetically instead of by scanning the whole window per week.
	perWeek := make([]float64, weeks)
	for _, e := range d.Entries {
		if !inGroup[e.ProjectID] || e.Date < fromISO || e.Date > toISO {
			continue
		}
		t, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			continue
		}
		wi := int(t.Sub(winStart).Hours()/24) / 7
		if wi < 0 || wi >= weeks {
			continue
		}
		perWeek[wi] += e.Hours
	}

	points := make([]BurnPoint, 0, weeks)
	cum := 0.0
	m := winStart
	for i := 0; i < weeks; i++ {
		cum += perWeek[i]
		yr, wk := m.ISOWeek()
		points = append(points, BurnPoint{
			ISOWeek:   wk,
			Month:     int(m.Month()),
			Year:      yr,
			Remaining: round1(budget - cum),
		})
		m = m.AddDate(0, 0, 7)
	}
	return points
}

var monthNames = []string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

var monthShort = []string{
	"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
	"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
}

// PeriodStat captures target vs. booked/forecast for one period (quarter/month).
type PeriodStat struct {
	Label       string
	Target      float64 // evenly split target for the period
	Forecast    float64 // project hours on today/future days in the period
	Actual      float64 // project hours on past days in the period (booked)
	Holiday     float64 // auto-booked holiday hours in the period (informational, not counted)
	Projected   float64 // all project hours in the period (booked + forecast)
	PctOfTarget float64 // projected / target * 100
}

// GoalSummary tracks fiscal-year target attainment. Only real (booked) and
// forecast project hours count towards the target. Public-holiday hours are
// reported separately but do NOT contribute to the goal.
type GoalSummary struct {
	HasTarget         bool
	StartLabel        string // FY start, e.g. 01.07.2026
	EndLabel          string // FY end, e.g. 30.06.2027
	TargetHours       float64
	ActualTotal       float64 // booked project hours (past days)
	ForecastRemaining float64 // forecast project hours (today and future days)
	HolidayHours      float64 // all weekday public-holiday hours in the FY (8h each) - informational
	HolidayDays       int
	Projected         float64 // all project hours (booked past + forecast future)
	Remaining         float64 // target - projected
	PctProjected      float64 // projected / target * 100
	PctActual         float64 // booked project hours / target * 100
	WorkingDaysYear   int
	WorkingDaysDone   int
	TargetPerWeek     float64 // target / number of FY weeks
	TargetPerMonth    float64 // target / 12
	TargetPerQuarter  float64 // target / 4
	Quarters          []PeriodStat
	Months            []PeriodStat
	Halves            []PeriodStat // H1 (first 6 FY months) and H2 (last 6)

	// Capacity overview (working time available in the FY).
	WeekdayHours      float64 // gross FY hours: weekdays (Mon-Fri) * 8h, or the configured override
	WeekdayHoursAuto  float64 // the calendar-derived value, before any override
	WeekdayDays       int     // number of weekdays in the FY
	VacationDays      int     // planned vacation days
	VacationHours     float64 // vacation days * 8h
	StandardTaskLabel string  // free-text label for recurring standard tasks
	StandardTaskHours float64 // hours deducted like holidays/vacation
	AvailableHours    float64 // WeekdayHours - HolidayHours - VacationHours - StandardTaskHours; this IS the target
	PctOfWeekdays     float64 // target / WeekdayHours * 100

	// Pace needed to still reach the goal from today onwards.
	RemainingGoal     float64 // target - actual booked (>= 0)
	RemainingWorkdays int     // remaining working days (weekdays minus holidays)
	RequiredPerDay    float64 // RemainingGoal / RemainingWorkdays
}

// FYCapacity breaks the hour budget of a fiscal year down from the gross
// weekday hours to the hours that actually have to be delivered - which are the
// FY goal. Unlike BuildGoalSummary it works for any fiscal year, not just the
// active one, so the settings page can show the same arithmetic.
type FYCapacity struct {
	WeekdayDays       int
	WeekdayHoursAuto  float64 // weekdays * 8h, straight from the calendar
	WeekdayHours      float64 // the configured override, or WeekdayHoursAuto
	HoursOverridden   bool    // true when a manual gross value is stored
	VacationDays      int
	VacationHours     float64
	HolidayDaysAuto   int // public holidays of the configured federal state
	HolidayDays       int // the configured override, or HolidayDaysAuto
	HolidayOverridden bool
	HolidayHours      float64
	StandardTaskLabel string
	StandardTaskHours float64
	RemainingHours    float64 // WeekdayHours - vacation - holidays - standard tasks
}

// BuildFYCapacity computes the capacity breakdown of the given fiscal year.
// The calendar must cover that year.
func BuildFYCapacity(d models.Data, cal *holidays.Calendar, year int) FYCapacity {
	fy := d.FYFor(year)
	fyStart, fyEnd := FiscalYear(year, normMonth(d.Settings.FiscalYearStartMonth))

	c := FYCapacity{
		VacationDays:      fy.VacationDays,
		StandardTaskLabel: fy.StandardTaskLabel,
		StandardTaskHours: round1(fy.StandardTaskHours),
	}
	for day := fyStart; !day.After(fyEnd); day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		c.WeekdayDays++
		if cal.IsHoliday(day.Format("2006-01-02")) {
			c.HolidayDaysAuto++
		}
	}
	c.WeekdayHoursAuto = round1(float64(c.WeekdayDays) * HolidayDayHours)
	c.WeekdayHours = c.WeekdayHoursAuto
	if fy.WeekdayHours > 0 {
		c.WeekdayHours = round1(fy.WeekdayHours)
		c.HoursOverridden = true
	}
	c.HolidayDays = c.HolidayDaysAuto
	if fy.HolidayDays != nil {
		c.HolidayDays = *fy.HolidayDays
		c.HolidayOverridden = true
	}
	c.HolidayHours = round1(float64(c.HolidayDays) * HolidayDayHours)
	c.VacationHours = round1(float64(c.VacationDays) * HolidayDayHours)
	c.RemainingHours = round1(c.WeekdayHours - c.VacationHours - c.HolidayHours - c.StandardTaskHours)
	if c.RemainingHours < 0 {
		c.RemainingHours = 0
	}
	return c
}

// BuildGoalSummary computes fiscal-year goal attainment. Each day carries a
// single hours value: days before today count as booked ("Ist"), today and
// future days as forecast. Public holidays on weekdays are reported separately
// but do NOT count towards the goal. Period targets are split evenly
// (target/4 per quarter, target/12 per month). The FY target is not stored: it
// is the net result of the hour configuration (BuildFYCapacity).
func BuildGoalSummary(d models.Data, cal *holidays.Calendar) GoalSummary {
	year := d.Settings.Year
	startMonth := normMonth(d.Settings.FiscalYearStartMonth)
	capacity := BuildFYCapacity(d, cal, year)
	target := capacity.RemainingHours
	fyStart, fyEnd := FiscalYear(year, startMonth)

	// Days strictly before today are booked; today and later are forecast.
	today := time.Now().UTC().Truncate(24 * time.Hour)

	hByDate := map[string]float64{}
	vac := vacationSet(d.Projects)
	known := knownProjects(d.Projects)
	for _, e := range d.Entries {
		if vac[e.ProjectID] || !known[e.ProjectID] {
			continue // vacation is informational; orphan entries belong to no project
		}
		hByDate[e.Date] += e.Hours
	}

	gs := GoalSummary{
		TargetHours: round1(target),
		HasTarget:   target > 0,
		StartLabel:  fyStart.Format("02.01.2006"),
		EndLabel:    fyEnd.Format("02.01.2006"),
	}
	quarters := make([]PeriodStat, 4)
	months := make([]PeriodStat, 12)

	for day := fyStart; !day.After(fyEnd); day = day.AddDate(0, 0, 1) {
		iso := day.Format("2006-01-02")
		// position within the fiscal year (0 = first FY month)
		fyMonth := (int(day.Month()) - startMonth + 12) % 12 // 0..11
		q := fyMonth / 3
		wd := day.Weekday()
		weekday := wd != time.Saturday && wd != time.Sunday
		isHoliday := weekday && cal.IsHoliday(iso)
		working := weekday && !isHoliday
		past := day.Before(today)
		if working {
			gs.WorkingDaysYear++
			if past {
				gs.WorkingDaysDone++
			}
		}

		// A single hours value per day: booked when past, forecast otherwise.
		h := hByDate[iso]
		var booked, forecast float64
		if past {
			booked = h
		} else {
			forecast = h
		}

		gs.ActualTotal += booked
		gs.ForecastRemaining += forecast
		gs.Projected += h      // all hours count; holidays do NOT count towards the goal
		gs.PctActual += booked // accumulate hours, converted to pct later

		quarters[q].Actual += booked
		quarters[q].Forecast += forecast
		if isHoliday {
			quarters[q].Holiday += HolidayDayHours
			months[fyMonth].Holiday += HolidayDayHours
		}
		quarters[q].Projected += h
		months[fyMonth].Actual += booked
		months[fyMonth].Forecast += forecast
		months[fyMonth].Projected += h
	}

	weeks := FYWeeks(year, startMonth)
	if weeks < 1 {
		weeks = 1
	}
	gs.TargetPerWeek = round1(target / float64(weeks))
	gs.TargetPerMonth = round1(target / 12)
	gs.TargetPerQuarter = round1(target / 4)

	for i := 0; i < 4; i++ {
		fm := (startMonth - 1 + i*3) % 12     // first calendar month of FY quarter (0..11)
		lm := (startMonth - 1 + i*3 + 2) % 12 // last calendar month
		quarters[i].Label = fmt.Sprintf("Q%d (%s–%s)", i+1, monthShort[fm], monthShort[lm])
		quarters[i].Target = round1(target / 4)
		quarters[i].Actual = round1(quarters[i].Actual)
		quarters[i].Forecast = round1(quarters[i].Forecast)
		quarters[i].Holiday = round1(quarters[i].Holiday)
		quarters[i].Projected = round1(quarters[i].Projected)
		if quarters[i].Target > 0 {
			quarters[i].PctOfTarget = round1(quarters[i].Projected / quarters[i].Target * 100)
		}
	}
	for i := 0; i < 12; i++ {
		cm := (startMonth - 1 + i) % 12
		months[i].Label = monthNames[cm]
		months[i].Target = round1(target / 12)
		months[i].Actual = round1(months[i].Actual)
		months[i].Forecast = round1(months[i].Forecast)
		months[i].Holiday = round1(months[i].Holiday)
		months[i].Projected = round1(months[i].Projected)
		if months[i].Target > 0 {
			months[i].PctOfTarget = round1(months[i].Projected / months[i].Target * 100)
		}
	}

	pctActualHours := gs.PctActual
	actualRaw := gs.ActualTotal
	gs.ActualTotal = round1(gs.ActualTotal)
	gs.ForecastRemaining = round1(gs.ForecastRemaining)
	gs.Projected = round1(gs.Projected)
	gs.Remaining = round1(target - gs.Projected)
	if target > 0 {
		gs.PctProjected = round1(gs.Projected / target * 100)
		gs.PctActual = round1(pctActualHours / target * 100)
	} else {
		gs.PctActual = 0
	}

	// Capacity overview: the configured breakdown, so an overridden gross value
	// or holiday count wins over the calendar here too.
	gs.WeekdayDays = capacity.WeekdayDays
	gs.WeekdayHoursAuto = capacity.WeekdayHoursAuto
	gs.WeekdayHours = capacity.WeekdayHours
	gs.VacationDays = capacity.VacationDays
	gs.VacationHours = capacity.VacationHours
	gs.HolidayDays = capacity.HolidayDays
	gs.HolidayHours = capacity.HolidayHours
	gs.StandardTaskLabel = capacity.StandardTaskLabel
	gs.StandardTaskHours = capacity.StandardTaskHours
	gs.AvailableHours = capacity.RemainingHours
	if gs.WeekdayHours > 0 {
		gs.PctOfWeekdays = round1(target / gs.WeekdayHours * 100)
	}

	// Pace required from today on to still reach the goal (real bookings only).
	gs.RemainingWorkdays = gs.WorkingDaysYear - gs.WorkingDaysDone
	rg := target - actualRaw
	if rg < 0 {
		rg = 0
	}
	gs.RemainingGoal = round1(rg)
	if gs.RemainingWorkdays > 0 {
		gs.RequiredPerDay = round1(rg / float64(gs.RemainingWorkdays))
	}

	gs.Quarters = quarters
	gs.Months = months

	// Half-year roll-ups (H1 = first 6 FY months, H2 = last 6), derived from the
	// per-month figures so they stay consistent with the month table.
	halves := make([]PeriodStat, 2)
	for half := 0; half < 2; half++ {
		var ps PeriodStat
		for m := half * 6; m < half*6+6; m++ {
			ps.Forecast += months[m].Forecast
			ps.Actual += months[m].Actual
			ps.Holiday += months[m].Holiday
			ps.Projected += months[m].Projected
		}
		fmH := (startMonth - 1 + half*6) % 12
		lmH := (startMonth - 1 + half*6 + 5) % 12
		ps.Label = fmt.Sprintf("%d. Halbjahr (%s–%s)", half+1, monthShort[fmH], monthShort[lmH])
		ps.Target = round1(target / 2)
		ps.Forecast = round1(ps.Forecast)
		ps.Actual = round1(ps.Actual)
		ps.Holiday = round1(ps.Holiday)
		ps.Projected = round1(ps.Projected)
		if ps.Target > 0 {
			ps.PctOfTarget = round1(ps.Projected / ps.Target * 100)
		}
		halves[half] = ps
	}
	gs.Halves = halves
	return gs
}

// --- Goal flow (project -> month -> quarter -> half-year -> fiscal year) ---

// GoalFlowStages names the columns of the goal flow diagram, left to right.
var GoalFlowStages = []string{"Projekte", "Monate", "Quartale", "Halbjahre", "Jahr"}

// Period nodes are coloured by how far the calendar has moved through them, so
// the diagram reads as a progress bar from left to right. Projects keep their
// own colour.
const (
	goalFlowDoneColor     = "#0e7490" // period lies completely in the past
	goalFlowCurrentColor  = "#1d4ed8" // period contains today
	goalFlowUpcomingColor = "#64748b" // period is still ahead
)

// FYMonthsDone reports how many months of the fiscal year are completely in the
// past: 0 before the fiscal year has started, 12 once it is over.
func FYMonthsDone(year, startMonth int) int {
	startMonth = normMonth(startMonth)
	fyStart, fyEnd := FiscalYear(year, startMonth)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	switch {
	case today.Before(fyStart):
		return 0
	case today.After(fyEnd):
		return 12
	default:
		return (int(today.Month()) - startMonth + 12) % 12
	}
}

// FYMonthProgress reports how far the fiscal year has progressed, measured in
// months with a fraction for the running one: 0 before it starts, 12 once it is
// over. The charts use it to place "today" between two month boundaries.
func FYMonthProgress(year, startMonth int) float64 {
	startMonth = normMonth(startMonth)
	fyStart, fyEnd := FiscalYear(year, startMonth)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	switch {
	case !today.After(fyStart):
		return 0
	case today.After(fyEnd):
		return 12
	}
	done := (int(today.Month()) - startMonth + 12) % 12
	monthStart := fyStart.AddDate(0, done, 0)
	monthEnd := fyStart.AddDate(0, done+1, 0)
	span := monthEnd.Sub(monthStart).Hours()
	if span <= 0 {
		return float64(done)
	}
	return float64(done) + today.Sub(monthStart).Hours()/span
}

// goalFlowState maps a period spanning the fiscal-year months [from, to) onto
// its progress colour and a label for the tooltip.
func goalFlowState(from, to, monthsDone int) (color, label string) {
	switch {
	case monthsDone >= to:
		return goalFlowDoneColor, "abgeschlossen"
	case monthsDone >= from:
		return goalFlowCurrentColor, "läuft"
	default:
		return goalFlowUpcomingColor, "steht noch aus"
	}
}

// GoalFlowNode is one box in a stage of the goal flow diagram.
type GoalFlowNode struct {
	ID          string
	Label       string  // short label drawn on/next to the node
	Title       string  // long label for the tooltip
	Color       string  // base colour; the booked share is drawn opaque on top
	Hours       float64 // planned hours (booked + forecast) flowing through
	Booked      float64 // share of Hours already booked (days before today)
	Target      float64 // evenly split goal for the period (0 for projects)
	PctOfTarget float64 // Hours / Target * 100
	StateLabel  string  // calendar progress of the period, empty for projects
}

// GoalFlowLink joins a node of one stage to a node of the next.
type GoalFlowLink struct {
	Stage     int // index of the source stage
	From, To  string
	FromLabel string
	ToLabel   string
	Color     string
	Hours     float64
}

// GoalFlow is the fiscal year's hours flowing from the individual projects
// through months and quarters into the half-years and finally the whole year.
// Vacation is excluded, exactly as in BuildGoalSummary, and only days inside the
// fiscal year are counted - so every stage sums up to the same total.
type GoalFlow struct {
	Stages  [][]GoalFlowNode
	Links   []GoalFlowLink
	Total   float64
	Booked  float64
	Target  float64
	HasData bool
}

// BuildGoalFlow aggregates the fiscal year's project hours into the five stages
// of the goal flow diagram.
func BuildGoalFlow(d models.Data, cal *holidays.Calendar) GoalFlow {
	year := d.Settings.Year
	startMonth := normMonth(d.Settings.FiscalYearStartMonth)
	fyStart, fyEnd := FiscalYear(year, startMonth)
	fyStartISO, fyEndISO := fyStart.Format("2006-01-02"), fyEnd.Format("2006-01-02")
	todayISO := todayISO()
	monthsDone := FYMonthsDone(year, startMonth)
	target := BuildFYCapacity(d, cal, year).RemainingHours

	vac := vacationSet(d.Projects)
	projByID := make(map[string]models.Project, len(d.Projects))
	for _, p := range d.Projects {
		projByID[p.ID] = p
	}

	type split struct{ hours, booked [12]float64 }
	byProject := map[string]*split{}
	var months, monthsBooked [12]float64
	for _, e := range d.Entries {
		if e.Hours <= 0 || vac[e.ProjectID] || e.Date < fyStartISO || e.Date > fyEndISO {
			continue
		}
		if _, ok := projByID[e.ProjectID]; !ok {
			continue
		}
		t, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			continue
		}
		m := (int(t.Month()) - startMonth + 12) % 12
		row, ok := byProject[e.ProjectID]
		if !ok {
			row = &split{}
			byProject[e.ProjectID] = row
		}
		row.hours[m] += e.Hours
		months[m] += e.Hours
		if e.Date < todayISO {
			row.booked[m] += e.Hours
			monthsBooked[m] += e.Hours
		}
	}

	flow := GoalFlow{Stages: make([][]GoalFlowNode, 5), Target: round1(target)}

	// Projects are ordered by their centre of gravity in the year, which keeps
	// the ribbons into the month column largely free of crossings.
	type projRow struct {
		p             models.Project
		hours, booked [12]float64
		total, done   float64
		centroid      float64
	}
	rows := make([]projRow, 0, len(byProject))
	for pid, s := range byProject {
		r := projRow{p: projByID[pid], hours: s.hours, booked: s.booked}
		var weighted float64
		for m, v := range r.hours {
			r.total += v
			r.done += r.booked[m]
			weighted += float64(m) * v
		}
		if r.total <= 0 {
			continue
		}
		r.centroid = weighted / r.total
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].centroid != rows[j].centroid {
			return rows[i].centroid < rows[j].centroid
		}
		return rows[i].p.Name < rows[j].p.Name
	})

	pct := func(hours, tgt float64) float64 {
		if tgt <= 0 {
			return 0
		}
		return round1(hours / tgt * 100)
	}

	for _, r := range rows {
		flow.Stages[0] = append(flow.Stages[0], GoalFlowNode{
			ID: "p:" + r.p.ID, Label: r.p.Name, Title: r.p.Name,
			Color: r.p.Color, Hours: round1(r.total), Booked: round1(r.done),
		})
		flow.Total += r.total
		flow.Booked += r.done
	}

	monthLabel := func(m int) (short, long string) {
		cm := (startMonth - 1 + m) % 12
		return monthShort[cm], monthNames[cm]
	}
	monthColors := [12]string{}
	quarterColors, halfColors := [4]string{}, [2]string{}
	var quarters, halves [4]float64
	var quartersBooked, halvesBooked [4]float64
	for m, v := range months {
		quarters[m/3] += v
		halves[m/6] += v
		quartersBooked[m/3] += monthsBooked[m]
		halvesBooked[m/6] += monthsBooked[m]
		color, state := goalFlowState(m, m+1, monthsDone)
		monthColors[m] = color
		if v <= 0 {
			continue
		}
		short, long := monthLabel(m)
		flow.Stages[1] = append(flow.Stages[1], GoalFlowNode{
			ID: fmt.Sprintf("m:%d", m), Label: short, Title: long,
			Color: color, Hours: round1(v), Booked: round1(monthsBooked[m]),
			Target: round1(target / 12), PctOfTarget: pct(v, target/12), StateLabel: state,
		})
	}
	for q := 0; q < 4; q++ {
		color, state := goalFlowState(q*3, q*3+3, monthsDone)
		quarterColors[q] = color
		if quarters[q] <= 0 {
			continue
		}
		fm, _ := monthLabel(q * 3)
		lm, _ := monthLabel(q*3 + 2)
		flow.Stages[2] = append(flow.Stages[2], GoalFlowNode{
			ID: fmt.Sprintf("q:%d", q), Label: fmt.Sprintf("Q%d", q+1),
			Title: fmt.Sprintf("Q%d (%s–%s)", q+1, fm, lm),
			Color: color, Hours: round1(quarters[q]), Booked: round1(quartersBooked[q]),
			Target: round1(target / 4), PctOfTarget: pct(quarters[q], target/4), StateLabel: state,
		})
	}
	for half := 0; half < 2; half++ {
		color, state := goalFlowState(half*6, half*6+6, monthsDone)
		halfColors[half] = color
		if halves[half] <= 0 {
			continue
		}
		fm, _ := monthLabel(half * 6)
		lm, _ := monthLabel(half*6 + 5)
		flow.Stages[3] = append(flow.Stages[3], GoalFlowNode{
			ID: fmt.Sprintf("h:%d", half), Label: fmt.Sprintf("H%d", half+1),
			Title: fmt.Sprintf("%d. Halbjahr (%s–%s)", half+1, fm, lm),
			Color: color, Hours: round1(halves[half]), Booked: round1(halvesBooked[half]),
			Target: round1(target / 2), PctOfTarget: pct(halves[half], target/2), StateLabel: state,
		})
	}
	if flow.Total > 0 {
		yearColor, yearState := goalFlowState(0, 12, monthsDone)
		flow.Stages[4] = []GoalFlowNode{{
			ID: "y", Label: fmt.Sprintf("FY %d", year), Title: fmt.Sprintf("Fiskaljahr %d", year),
			Color: yearColor, Hours: round1(flow.Total), Booked: round1(flow.Booked),
			Target: round1(target), PctOfTarget: pct(flow.Total, target), StateLabel: yearState,
		}}
	}

	for _, r := range rows {
		for m, v := range r.hours {
			if v <= 0 {
				continue
			}
			short, _ := monthLabel(m)
			flow.Links = append(flow.Links, GoalFlowLink{
				Stage: 0, From: "p:" + r.p.ID, To: fmt.Sprintf("m:%d", m),
				FromLabel: r.p.Name, ToLabel: short, Color: r.p.Color, Hours: round1(v),
			})
		}
	}
	for m, v := range months {
		if v <= 0 {
			continue
		}
		short, _ := monthLabel(m)
		flow.Links = append(flow.Links, GoalFlowLink{
			Stage: 1, From: fmt.Sprintf("m:%d", m), To: fmt.Sprintf("q:%d", m/3),
			FromLabel: short, ToLabel: fmt.Sprintf("Q%d", m/3+1),
			Color: monthColors[m], Hours: round1(v),
		})
	}
	for q := 0; q < 4; q++ {
		if quarters[q] <= 0 {
			continue
		}
		flow.Links = append(flow.Links, GoalFlowLink{
			Stage: 2, From: fmt.Sprintf("q:%d", q), To: fmt.Sprintf("h:%d", q/2),
			FromLabel: fmt.Sprintf("Q%d", q+1), ToLabel: fmt.Sprintf("H%d", q/2+1),
			Color: quarterColors[q], Hours: round1(quarters[q]),
		})
	}
	for half := 0; half < 2; half++ {
		if halves[half] <= 0 {
			continue
		}
		flow.Links = append(flow.Links, GoalFlowLink{
			Stage: 3, From: fmt.Sprintf("h:%d", half), To: "y",
			FromLabel: fmt.Sprintf("H%d", half+1), ToLabel: fmt.Sprintf("FY %d", year),
			Color: halfColors[half], Hours: round1(halves[half]),
		})
	}

	flow.Total = round1(flow.Total)
	flow.Booked = round1(flow.Booked)
	flow.HasData = flow.Total > 0
	return flow
}

// --- Dashboard utilization Sankey (time flow) ---

// SankeyRange defines one selectable horizon for the dashboard Sankey diagram.
type SankeyRange struct {
	Key   string // stable query value (see SankeyRanges)
	Label string // button label
}

// SankeyRanges lists the horizons offered by the toggles above the dashboard
// Sankey, in display order.
var SankeyRanges = []SankeyRange{
	{Key: "1w", Label: "1 Woche"},
	{Key: "2w", Label: "2 Wochen"},
	{Key: "4w", Label: "4 Wochen"},
	{Key: "2m", Label: "2 Monate"},
	{Key: "3m", Label: "3 Monate"},
	{Key: "6m", Label: "Halbjahr"},
	{Key: "fy", Label: "Fiskaljahr"},
}

// SankeyDefaultRange is the horizon used when none (or an unknown one) is
// requested.
const SankeyDefaultRange = "4w"

// SankeyMaxOffset bounds how far the horizon may be shifted into the past or
// future, so a hand-crafted query parameter cannot cause pointless work.
const SankeyMaxOffset = 260

// NormalizeSankeyRange returns key when it is a known range, else the default.
func NormalizeSankeyRange(key string) string {
	for _, r := range SankeyRanges {
		if r.Key == key {
			return key
		}
	}
	return SankeyDefaultRange
}

// SankeyBucket is one week column of the utilization flow. Total is the summed
// planned project hours in the week; Hours holds the per-project split that
// drives the stacked bands. The capacity figures make the remaining free time
// of the week explicit.
type SankeyBucket struct {
	Label         string             // primary axis label (e.g. "KW30")
	SubLabel      string             // secondary label (Monday's date)
	Total         float64            // summed planned hours over all projects
	Hours         map[string]float64 // projectID -> planned hours in this bucket
	WeekdayHours  float64            // in-FY weekdays (Mon-Fri) in the bucket * 8h
	HolidayHours  float64            // public holidays among those weekdays * 8h
	CapacityHours float64            // WeekdayHours - HolidayHours
	FreeHours     float64            // CapacityHours - Total (negative = overbooked)
}

// SankeyData is the dashboard utilization time-flow. Buckets are evenly spaced
// week columns drawn across the full width; each project forms a coloured band
// whose height is proportional to its planned hours, connected by ribbons
// between adjacent weeks. The vacation project is an ordinary band here: a
// vacation week shows the other projects flowing into the grey vacation band
// and back out again afterwards.
type SankeyData struct {
	RangeKey      string             // selected range key
	Buckets       []SankeyBucket     // week columns, left to right
	Projects      []models.Project   // projects with hours in the span, in stack order
	ProjectTotals map[string]float64 // projectID -> total hours over the span
	Total         float64            // grand total planned hours over the span
	MaxBucket     float64            // largest single-bucket total (vertical scale)
	RangeLabel    string             // whole-span range, DD.MM.YYYY – DD.MM.YYYY

	// Horizon shifting: the span can be moved backwards/forwards in whole spans
	// so past weeks/months can be reviewed.
	Offset     int  // applied shift in spans (0 = the current one)
	PrevOffset int  // offset of the previous span
	NextOffset int  // offset of the next span
	CanPrev    bool // false when the span already starts at the FY start
	CanNext    bool // false when the span already ends at the FY end

	// Capacity roll-up over the whole span (drives the free-time chart).
	CapacityTotal float64 // available working hours over the span
	FreeTotal     float64 // CapacityTotal - Total
	MaxFree       float64 // largest absolute per-bucket free/overbooked value
}

// sankeySpan resolves a range key into a fiscal-year week window (1-based start
// week and week count). Every range is bucketed by ISO week, no matter how long
// it is: the weekly rate is what drives the forecast accuracy, so a monthly
// column would hide exactly the figure the chart exists for.
func sankeySpan(year, startMonth, curWeek int, key string) (startWeek, weeks int) {
	maxW := FYWeeks(year, startMonth)
	switch key {
	case "1w":
		return curWeek, 1
	case "2w":
		return curWeek, 2
	case "4w":
		return curWeek, 4
	case "2m":
		return curWeek, 8
	case "3m":
		return curWeek, 13
	case "6m":
		half := (maxW + 1) / 2
		if curWeek <= half {
			return 1, half
		}
		return half + 1, maxW - half
	case "fy":
		return 1, maxW
	default:
		return curWeek, 4
	}
}

// shiftSankeySpan moves a span of `weeks` weeks starting at baseWeek by `offset`
// whole spans and clamps the result into the fiscal year. It returns the
// resulting start week together with the offset that actually took effect, so
// the navigation links never point outside the fiscal year.
func shiftSankeySpan(baseWeek, weeks, maxW, offset int) (startWeek, applied int) {
	if weeks < 1 {
		weeks = 1
	}
	if offset > SankeyMaxOffset {
		offset = SankeyMaxOffset
	}
	if offset < -SankeyMaxOffset {
		offset = -SankeyMaxOffset
	}
	maxStart := maxW - weeks + 1
	if maxStart < 1 {
		maxStart = 1
	}
	if baseWeek < 1 {
		baseWeek = 1
	}
	if baseWeek > maxStart {
		baseWeek = maxStart
	}
	// Clamp flush against the fiscal-year border (rather than stepping back a
	// whole span) so the last span ends exactly at the FY end and CanPrev/CanNext
	// turn off precisely there.
	startWeek = baseWeek + offset*weeks
	if startWeek < 1 {
		startWeek = 1
	}
	if startWeek > maxStart {
		startWeek = maxStart
	}
	// Report the offset that actually took effect. A clamped span that is still
	// off the default start counts as shifted, so the UI can offer a way back.
	delta := startWeek - baseWeek
	applied = delta / weeks
	if applied == 0 && delta != 0 {
		applied = -1
		if delta > 0 {
			applied = 1
		}
	}
	return startWeek, applied
}

// BuildSankey aggregates planned project hours into week buckets over the
// horizon selected by rangeKey, for the dashboard utilization Sankey. The
// horizon can be shifted by whole spans via offset (negative = into the past).
// Only days within the fiscal year are counted; the vacation project is treated
// like any other project and forms its own band.
func BuildSankey(d models.Data, cal *holidays.Calendar, rangeKey string, offset int) SankeyData {
	year := d.Settings.Year
	startMonth := normMonth(d.Settings.FiscalYearStartMonth)
	rangeKey = NormalizeSankeyRange(rangeKey)
	cur := CurrentFYWeek(year, startMonth)
	maxW := FYWeeks(year, startMonth)
	baseWeek, weeks := sankeySpan(year, startMonth, cur, rangeKey)
	if weeks < 1 {
		weeks = 1
	}
	if weeks > maxW {
		weeks = maxW
	}
	startWeek, offset := shiftSankeySpan(baseWeek, weeks, maxW, offset)

	fyStart, fyEnd := FiscalYear(year, startMonth)
	fyStartISO := fyStart.Format("2006-01-02")
	fyEndISO := fyEnd.Format("2006-01-02")
	hidx := hoursIndex(d.Entries)

	data := SankeyData{
		RangeKey:      rangeKey,
		ProjectTotals: map[string]float64{},
		Offset:        offset,
		PrevOffset:    offset - 1,
		NextOffset:    offset + 1,
		CanPrev:       startWeek > 1,
		CanNext:       startWeek+weeks-1 < maxW,
	}

	// add accumulates one in-FY weekday's per-project hours and capacity into a
	// bucket and tracks the visible date span for the range label.
	var firstISO, lastISO string
	add := func(b *SankeyBucket, iso string) {
		if iso < fyStartISO || iso > fyEndISO {
			return
		}
		if firstISO == "" {
			firstISO = iso
		}
		lastISO = iso
		b.WeekdayHours += HolidayDayHours
		if cal.IsHoliday(iso) {
			b.HolidayHours += HolidayDayHours
		}
		for _, p := range d.Projects {
			h := hidx[iso+"|"+p.ID]
			if h == 0 {
				continue
			}
			b.Hours[p.ID] += h
			b.Total += h
			data.ProjectTotals[p.ID] += h
		}
	}

	for wi := 0; wi < weeks; wi++ {
		monday := FYWeekMonday(year, startMonth, startWeek+wi)
		_, iso := monday.ISOWeek()
		bucket := SankeyBucket{
			Label:    fmt.Sprintf("KW%02d", iso),
			SubLabel: monday.Format("02.01."),
			Hours:    map[string]float64{},
		}
		for i := 0; i < 5; i++ {
			add(&bucket, monday.AddDate(0, 0, i).Format("2006-01-02"))
		}
		bucket.Total = round1(bucket.Total)
		data.Buckets = append(data.Buckets, bucket)
	}

	// projects present in the span, ordered by total desc then name (stable
	// stack order across all buckets)
	projByID := map[string]models.Project{}
	for _, p := range d.Projects {
		projByID[p.ID] = p
	}
	present := make([]models.Project, 0, len(data.ProjectTotals))
	for pid, tot := range data.ProjectTotals {
		if tot <= 0 {
			continue
		}
		if p, ok := projByID[pid]; ok {
			present = append(present, p)
		}
	}
	sort.Slice(present, func(i, j int) bool {
		ti, tj := data.ProjectTotals[present[i].ID], data.ProjectTotals[present[j].ID]
		if ti != tj {
			return ti > tj
		}
		return present[i].Name < present[j].Name
	})
	data.Projects = present

	for pid, tot := range data.ProjectTotals {
		data.ProjectTotals[pid] = round1(tot)
	}
	for i := range data.Buckets {
		bk := &data.Buckets[i]
		bk.CapacityHours = round1(bk.WeekdayHours - bk.HolidayHours)
		bk.FreeHours = round1(bk.CapacityHours - bk.Total)

		data.Total += bk.Total
		data.CapacityTotal += bk.CapacityHours
		if bk.Total > data.MaxBucket {
			data.MaxBucket = bk.Total
		}
		if f := bk.FreeHours; f > data.MaxFree {
			data.MaxFree = f
		} else if -f > data.MaxFree {
			data.MaxFree = -f
		}
	}
	data.Total = round1(data.Total)
	data.CapacityTotal = round1(data.CapacityTotal)
	data.FreeTotal = round1(data.CapacityTotal - data.Total)
	if firstISO != "" {
		data.RangeLabel = formatDayDot(firstISO) + " – " + formatDayDot(lastISO)
	}
	return data
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
