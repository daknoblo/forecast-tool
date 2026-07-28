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
			wv.HolidayHours += HolidayDayHours
		}
		for _, p := range d.Projects {
			h := hidx[iso+"|"+p.ID]
			if h != 0 {
				cell.Hours[p.ID] = h
				cell.Total += h
				wv.ProjectTotals[p.ID] += h
			}
		}
		wv.Total += cell.Total
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
func BuildSpanBurn(ps []ProjectSummary, spanStart, spanEnd string) SpanBurn {
	var sb SpanBurn
	for _, p := range ps {
		if !p.Project.Active {
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
	CarryOver       float64   // hours of this assignment dated in earlier fiscal years
	FutureFY        float64   // hours of this assignment dated in later fiscal years
	FYSplit         []FYHours // hours per fiscal year, ascending (only years with hours)
	SpansFY         bool      // the assignment has hours in more than one fiscal year
	AvailableBudget float64   // BudgetHours - CarryOver (what is left for this FY)

	Remaining      float64 // AvailableBudget - Consumed
	UtilizationPct float64 // (CarryOver + Consumed) / budget * 100
	CarryOverPct   float64 // CarryOver / budget * 100
	ForecastPct    float64 // forecast / budget * 100
	ActualPct      float64 // booked / budget * 100

	// Booking window (clamped to the fiscal year). Empty project dates default
	// to the FY bounds.
	StartDate       string // effective window start, ISO YYYY-MM-DD
	EndDate         string // effective window end, ISO YYYY-MM-DD
	StartLabel      string // DD.MM.YYYY
	EndLabel        string // DD.MM.YYYY
	HasCustomWindow bool   // true if the project sets an explicit start or end
	RemainingLabel  string // time left until the window end, e.g. "2 Wochen und 3 Tage"

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
type YearSummary struct {
	Projects       []ProjectSummary
	TotalHours     float64 // all hours dated inside the fiscal year
	TotalBudget    float64 // summed budget of all projects
	TotalCarryOver float64 // summed hours carried over from earlier fiscal years
	TotalAvailable float64 // TotalBudget - TotalCarryOver (usable in this fiscal year)
	TotalForecast  float64 // summed forecast hours (today and later)
	TotalActual    float64 // summed booked hours (past days)
	HasCarryOver   bool    // true when at least one project carries hours over
	HasFYSplit     bool    // true when at least one assignment spans fiscal years
	WeekTotals     []WeekTotal
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
// a compact German form, e.g. "7 Tage", "2 Wochen und 3 Tage" or "3 Monate".
func remainingLabel(from, to time.Time) string {
	days := int(to.Sub(from).Hours()/24) + 1
	switch {
	case days <= 0:
		return "abgelaufen"
	case days == 1:
		return "1 Tag"
	case days < 7:
		return fmt.Sprintf("%d Tage", days)
	case days < 60:
		weeks, rest := days/7, days%7
		out := fmt.Sprintf("%d Wochen", weeks)
		if weeks == 1 {
			out = "1 Woche"
		}
		switch {
		case rest == 1:
			out += " und 1 Tag"
		case rest > 1:
			out += fmt.Sprintf(" und %d Tage", rest)
		}
		return out
	default:
		return fmt.Sprintf("%d Monate", (days+15)/30)
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

		if p, ok := projByID[pid]; ok {
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
		rem := avail - c
		util, coPct, fPct, aPct := 0.0, 0.0, 0.0, 0.0
		if p.BudgetHours > 0 {
			util = round1((over + c) / p.BudgetHours * 100)
			coPct = round1(over / p.BudgetHours * 100)
			fPct = round1(forecastByGroup[g] / p.BudgetHours * 100)
			aPct = round1(actualByGroup[g] / p.BudgetHours * 100)
		}

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
			AvailableBudget:    round1(avail),
			Remaining:          round1(rem),
			UtilizationPct:     util,
			CarryOverPct:       coPct,
			ForecastPct:        fPct,
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
		ys.TotalHours += c
		ys.TotalBudget += p.BudgetHours
		ys.TotalCarryOver += over
		ys.TotalForecast += forecastByGroup[g]
		ys.TotalActual += actualByGroup[g]
		if over > 0 {
			ys.HasCarryOver = true
		}
		if len(split) > 1 {
			ys.HasFYSplit = true
		}
	}
	ys.TotalHours = round1(ys.TotalHours)
	ys.TotalBudget = round1(ys.TotalBudget)
	ys.TotalCarryOver = round1(ys.TotalCarryOver)
	ys.TotalAvailable = round1(ys.TotalBudget - ys.TotalCarryOver)
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
	}
	return ys
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
// year (they are already deducted from the budget as a carry-over).
func BuildBurndown(d models.Data, projectID, startISO, endISO string, budget float64) []BurnPoint {
	start, errS := time.Parse("2006-01-02", startISO)
	end, errE := time.Parse("2006-01-02", endISO)
	if errS != nil || errE != nil || end.Before(start) {
		// Fallback: span the whole fiscal year.
		start, end = FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	}
	fromISO := start.Format("2006-01-02")
	toISO := end.Format("2006-01-02")

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
		if e.ProjectID != projectID || e.Date < fromISO || e.Date > toISO {
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
	WeekdayHours      float64 // all FY weekdays (Mon-Fri) * 8h, weekends excluded
	WeekdayDays       int     // number of weekdays in the FY
	VacationDays      int     // planned vacation days (H1 + H2)
	VacationHours     float64 // vacation days * 8h
	VacationDaysH1    int     // vacation days in the first FY half
	VacationDaysH2    int     // vacation days in the second FY half
	VacationHoursH1   float64
	VacationHoursH2   float64
	StandardTaskLabel string  // free-text label for recurring standard tasks
	StandardTaskHours float64 // hours deducted like holidays/vacation
	AvailableHours    float64 // WeekdayHours - HolidayHours - VacationHours - StandardTaskHours
	PctOfWeekdays     float64 // target / WeekdayHours * 100
	PctOfAvailable    float64 // target / AvailableHours * 100

	// Pace needed to still reach the goal from today onwards.
	RemainingGoal     float64 // target - actual booked (>= 0)
	RemainingWorkdays int     // remaining working days (weekdays minus holidays)
	RequiredPerDay    float64 // RemainingGoal / RemainingWorkdays
}

// BuildGoalSummary computes fiscal-year goal attainment. Each day carries a
// single hours value: days before today count as booked ("Ist"), today and
// future days as forecast. Public holidays on weekdays are reported separately
// but do NOT count towards the goal. Period targets are split evenly
// (target/4 per quarter, target/12 per month).
func BuildGoalSummary(d models.Data, cal *holidays.Calendar) GoalSummary {
	year := d.Settings.Year
	startMonth := normMonth(d.Settings.FiscalYearStartMonth)
	fy := d.CurrentFY()
	target := fy.TargetHours
	fyStart, fyEnd := FiscalYear(year, startMonth)

	// Days strictly before today are booked; today and later are forecast.
	today := time.Now().UTC().Truncate(24 * time.Hour)

	hByDate := map[string]float64{}
	vac := vacationSet(d.Projects)
	for _, e := range d.Entries {
		if vac[e.ProjectID] {
			continue // vacation is informational, not counted towards the goal
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
	weekdayDays := 0

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
		if weekday {
			weekdayDays++
		}
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
		if isHoliday {
			gs.HolidayDays++
			gs.HolidayHours += HolidayDayHours
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
	gs.HolidayHours = round1(gs.HolidayHours)
	gs.Projected = round1(gs.Projected)
	gs.Remaining = round1(target - gs.Projected)
	if target > 0 {
		gs.PctProjected = round1(gs.Projected / target * 100)
		gs.PctActual = round1(pctActualHours / target * 100)
	} else {
		gs.PctActual = 0
	}

	// Capacity overview: gross weekday hours minus holidays, planned vacation
	// (per half-year) and recurring standard tasks.
	gs.WeekdayDays = weekdayDays
	gs.WeekdayHours = round1(float64(weekdayDays) * HolidayDayHours)
	gs.VacationDaysH1 = fy.VacationDaysH1
	gs.VacationDaysH2 = fy.VacationDaysH2
	gs.VacationDays = fy.VacationDaysH1 + fy.VacationDaysH2
	gs.VacationHoursH1 = round1(float64(fy.VacationDaysH1) * HolidayDayHours)
	gs.VacationHoursH2 = round1(float64(fy.VacationDaysH2) * HolidayDayHours)
	gs.VacationHours = round1(float64(gs.VacationDays) * HolidayDayHours)
	gs.StandardTaskLabel = fy.StandardTaskLabel
	gs.StandardTaskHours = round1(fy.StandardTaskHours)
	gs.AvailableHours = round1(gs.WeekdayHours - gs.HolidayHours - gs.VacationHours - gs.StandardTaskHours)
	if gs.WeekdayHours > 0 {
		gs.PctOfWeekdays = round1(target / gs.WeekdayHours * 100)
	}
	if gs.AvailableHours > 0 {
		gs.PctOfAvailable = round1(target / gs.AvailableHours * 100)
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

// SankeyBucket is one time column (a week or a month) of the utilization flow.
// Total is the summed planned project hours in the bucket; Hours holds the
// per-project split that drives the stacked bands. The capacity figures make
// the remaining free time of the bucket explicit.
type SankeyBucket struct {
	Label         string             // primary axis label (e.g. "KW30" or "Jul")
	SubLabel      string             // secondary label (start date or year)
	Total         float64            // summed planned hours over all projects
	Hours         map[string]float64 // projectID -> planned hours in this bucket
	WeekdayHours  float64            // in-FY weekdays (Mon-Fri) in the bucket * 8h
	HolidayHours  float64            // public holidays among those weekdays * 8h
	VacationHours float64            // planned vacation hours in the bucket
	VacationDays  float64            // VacationHours / 8h
	CapacityHours float64            // WeekdayHours - HolidayHours - VacationHours
	FreeHours     float64            // CapacityHours - Total (negative = overbooked)
}

// SankeyData is the dashboard utilization time-flow. Buckets are evenly spaced
// columns (weeks or months) drawn across the full width; each project forms a
// coloured band whose height is proportional to its planned hours, connected by
// ribbons between adjacent buckets. The vacation project is not part of the
// bands (it is reported per bucket instead) and never counts towards Total.
type SankeyData struct {
	RangeKey      string             // selected range key
	Unit          string             // "week" | "month" bucket granularity
	Buckets       []SankeyBucket     // time columns, left to right
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
	VacationTotal float64 // planned vacation hours over the span
	CapacityTotal float64 // available working hours over the span
	FreeTotal     float64 // CapacityTotal - Total
	MaxFree       float64 // largest absolute per-bucket free/overbooked value
}

// sankeySpan resolves a range key into a fiscal-year week window (1-based start
// week and week count) and the bucket unit ("week" or "month").
func sankeySpan(year, startMonth, curWeek int, key string) (startWeek, weeks int, unit string) {
	maxW := FYWeeks(year, startMonth)
	switch key {
	case "1w":
		return curWeek, 1, "week"
	case "2w":
		return curWeek, 2, "week"
	case "4w":
		return curWeek, 4, "week"
	case "2m":
		return curWeek, 8, "week"
	case "3m":
		return curWeek, 13, "month"
	case "6m":
		half := (maxW + 1) / 2
		if curWeek <= half {
			return 1, half, "month"
		}
		return half + 1, maxW - half, "month"
	case "fy":
		return 1, maxW, "month"
	default:
		return curWeek, 4, "week"
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

// BuildSankey aggregates planned project hours into week or month buckets over
// the horizon selected by rangeKey, for the dashboard utilization Sankey. The
// horizon can be shifted by whole spans via offset (negative = into the past).
// Only days within the fiscal year are counted; vacation hours are reported per
// bucket instead of being part of the stacked bands.
func BuildSankey(d models.Data, cal *holidays.Calendar, rangeKey string, offset int) SankeyData {
	year := d.Settings.Year
	startMonth := normMonth(d.Settings.FiscalYearStartMonth)
	rangeKey = NormalizeSankeyRange(rangeKey)
	cur := CurrentFYWeek(year, startMonth)
	maxW := FYWeeks(year, startMonth)
	baseWeek, weeks, unit := sankeySpan(year, startMonth, cur, rangeKey)
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
	vac := vacationSet(d.Projects)

	data := SankeyData{
		RangeKey:      rangeKey,
		Unit:          unit,
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
			if vac[p.ID] {
				b.VacationHours += h
				continue
			}
			b.Hours[p.ID] += h
			b.Total += h
			data.ProjectTotals[p.ID] += h
		}
	}

	if unit == "week" {
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
	} else {
		idxOf := map[string]int{}
		for wi := 0; wi < weeks; wi++ {
			monday := FYWeekMonday(year, startMonth, startWeek+wi)
			for i := 0; i < 5; i++ {
				day := monday.AddDate(0, 0, i)
				iso := day.Format("2006-01-02")
				if iso < fyStartISO || iso > fyEndISO {
					continue
				}
				monthKey := day.Format("2006-01")
				bi, ok := idxOf[monthKey]
				if !ok {
					bi = len(data.Buckets)
					idxOf[monthKey] = bi
					sub := ""
					if day.Month() == time.January || bi == 0 {
						sub = fmt.Sprintf("%d", day.Year())
					}
					data.Buckets = append(data.Buckets, SankeyBucket{
						Label:    monthShort[int(day.Month())-1],
						SubLabel: sub,
						Hours:    map[string]float64{},
					})
				}
				add(&data.Buckets[bi], iso)
			}
		}
		for i := range data.Buckets {
			data.Buckets[i].Total = round1(data.Buckets[i].Total)
		}
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
		bk.VacationHours = round1(bk.VacationHours)
		bk.VacationDays = round1(bk.VacationHours / HolidayDayHours)
		bk.CapacityHours = round1(bk.WeekdayHours - bk.HolidayHours - bk.VacationHours)
		bk.FreeHours = round1(bk.CapacityHours - bk.Total)

		data.Total += bk.Total
		data.VacationTotal += bk.VacationHours
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
	data.VacationTotal = round1(data.VacationTotal)
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
