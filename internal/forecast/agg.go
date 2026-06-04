package forecast

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// DayCell holds the forecast for a single day across all projects.
type DayCell struct {
	Date         string             // YYYY-MM-DD
	WeekdayName  string             // Mo, Di, ...
	InYear       bool               // belongs to the configured fiscal year
	IsHoliday    bool               // public holiday
	HolidayName  string             // holiday label, if any
	HolidayHours float64            // auto-booked hours for a weekday holiday (8h)
	Hours        map[string]float64 // projectID -> forecast hours
	Actual       map[string]float64 // projectID -> actual (booked) hours
	Total        float64            // forecast sum over projects
	ActualTotal  float64            // actual sum over projects
}

// WeekView aggregates a single fiscal-year week (Mon-Fri).
type WeekView struct {
	Year                 int
	Week                 int // 1-based fiscal-year week index
	ISOWeek              int
	Label                string
	RangeLabel           string
	Days                 []DayCell
	ProjectTotals        map[string]float64
	ActualTotals         map[string]float64
	Total                float64
	ActualTotal          float64
	EffectiveTotal       float64 // actual where booked, else forecast (basis for the status)
	HolidayHours         float64
	TargetHours          float64
	UtilizationPct       float64
	ActualUtilizationPct float64
	Status               models.UtilStatus // booking traffic-light for this week
	PrevWeek             int
	NextWeek             int
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
// anchored at the given year and start month. With startMonth==1 it equals the
// calendar year.
func FiscalYear(year, startMonth int) (time.Time, time.Time) {
	startMonth = normMonth(startMonth)
	start := time.Date(year, time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
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

// BuildWeek assembles the Mon-Fri view for one fiscal-year week.
func BuildWeek(d models.Data, cal *holidays.Calendar, week int) WeekView {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	monday := FYWeekMonday(year, startMonth, week)
	fyStart, fyEnd := FiscalYear(year, startMonth)
	fidx := kindIndex(d.Entries, models.KindForecast)
	aidx := kindIndex(d.Entries, models.KindActual)

	_, isoWeek := monday.ISOWeek()
	friday := monday.AddDate(0, 0, 4)
	wv := WeekView{
		Year:          year,
		Week:          week,
		ISOWeek:       isoWeek,
		Label:         fmt.Sprintf("Woche %d · KW %02d", week, isoWeek),
		RangeLabel:    monday.Format("02.01.") + "–" + friday.Format("02.01.2006"),
		ProjectTotals: map[string]float64{},
		ActualTotals:  map[string]float64{},
		TargetHours:   d.Settings.WeeklyTargetHours,
		PrevWeek:      week - 1,
		NextWeek:      week + 1,
	}

	for i := 0; i < 5; i++ {
		day := monday.AddDate(0, 0, i)
		iso := day.Format("2006-01-02")
		inYear := !day.Before(fyStart) && !day.After(fyEnd)
		cell := DayCell{
			Date:        iso,
			WeekdayName: weekdayNames[i],
			InYear:      inYear,
			IsHoliday:   cal.IsHoliday(iso),
			HolidayName: cal.Name(iso),
			Hours:       map[string]float64{},
			Actual:      map[string]float64{},
		}
		if cell.IsHoliday {
			cell.HolidayHours = HolidayDayHours
			wv.HolidayHours += HolidayDayHours
		}
		for _, p := range d.Projects {
			f := fidx[iso+"|"+p.ID]
			a := aidx[iso+"|"+p.ID]
			if f != 0 {
				cell.Hours[p.ID] = f
				cell.Total += f
				wv.ProjectTotals[p.ID] += f
			}
			if a != 0 {
				cell.Actual[p.ID] = a
				cell.ActualTotal += a
				wv.ActualTotals[p.ID] += a
			}
			// Effective hours: a booked actual overrides the forecast.
			if a != 0 {
				wv.EffectiveTotal += a
			} else {
				wv.EffectiveTotal += f
			}
		}
		wv.Total += cell.Total
		wv.ActualTotal += cell.ActualTotal
		wv.Days = append(wv.Days, cell)
	}

	wv.EffectiveTotal = round1(wv.EffectiveTotal)
	wv.Status = d.Settings.ClassifyUtilization(wv.EffectiveTotal)
	if wv.TargetHours > 0 {
		wv.UtilizationPct = round1(wv.Total / wv.TargetHours * 100)
		wv.ActualUtilizationPct = round1(wv.ActualTotal / wv.TargetHours * 100)
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

// SpanView aggregates several consecutive fiscal-year weeks into one Mon-Fri
// grid so wide screens can show as many days as fit at once.
type SpanView struct {
	StartWeek            int
	EndWeek              int
	Weeks                int
	MaxWeek              int
	PrevStart            int
	NextStart            int
	RangeLabel           string
	Blocks               []WeekView         // one entry per visible week (for header grouping)
	Days                 []DayCell          // all days flattened across the visible weeks
	ProjectTotals        map[string]float64 // projectID -> forecast hours over the span
	ActualTotals         map[string]float64 // projectID -> actual hours over the span
	Total                float64
	ActualTotal          float64
	HolidayHours         float64
	TargetHours          float64 // weekly target * number of visible weeks
	UtilizationPct       float64
	ActualUtilizationPct float64
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
		ActualTotals:  map[string]float64{},
	}
	for i := 0; i < weeks; i++ {
		wv := BuildWeek(d, cal, startWeek+i)
		sv.Blocks = append(sv.Blocks, wv)
		sv.Days = append(sv.Days, wv.Days...)
		for pid, h := range wv.ProjectTotals {
			sv.ProjectTotals[pid] += h
		}
		for pid, h := range wv.ActualTotals {
			sv.ActualTotals[pid] += h
		}
		sv.Total += wv.Total
		sv.ActualTotal += wv.ActualTotal
		sv.HolidayHours += wv.HolidayHours
	}
	sv.Total = round1(sv.Total)
	sv.ActualTotal = round1(sv.ActualTotal)
	sv.HolidayHours = round1(sv.HolidayHours)
	sv.TargetHours = round1(d.Settings.WeeklyTargetHours * float64(weeks))
	if sv.TargetHours > 0 {
		sv.UtilizationPct = round1(sv.Total / sv.TargetHours * 100)
		sv.ActualUtilizationPct = round1(sv.ActualTotal / sv.TargetHours * 100)
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

// ProjectSummary describes budget consumption for one project.
type ProjectSummary struct {
	Project        models.Project
	Forecast       float64 // planned hours
	Actual         float64 // actually booked hours
	Consumed       float64 // effective: actual where booked, otherwise forecast
	Remaining      float64 // budget - consumed (effective)
	UtilizationPct float64 // consumed / budget * 100
}

// YearSummary aggregates all projects and weekly totals for the fiscal year.
type YearSummary struct {
	Projects   []ProjectSummary
	TotalHours float64 // effective hours over all projects
	WeekTotals []WeekTotal
}

// WeekTotal is the summed effective hours for a single fiscal-year week.
type WeekTotal struct {
	Week           int // fiscal-year week index
	ISOWeek        int
	Label          string
	Hours          float64
	TargetHours    float64
	UtilizationPct float64
	Status         models.UtilStatus // booking traffic-light for this week
}

// effectiveByKey returns per "date|projectId" the effective hours, where a
// booked actual value overrides the forecast for that day and project.
func effectiveByKey(entries []models.Entry) map[string]float64 {
	forecast := map[string]float64{}
	actual := map[string]float64{}
	hasActual := map[string]bool{}
	for _, e := range entries {
		k := e.Date + "|" + e.ProjectID
		if entryKind(e) == models.KindActual {
			actual[k] += e.Hours
			hasActual[k] = true
		} else {
			forecast[k] += e.Hours
		}
	}
	eff := make(map[string]float64, len(forecast)+len(actual))
	for k, v := range forecast {
		eff[k] = v
	}
	for k := range hasActual {
		eff[k] = actual[k]
	}
	return eff
}

// BuildYearSummary computes per-project effective consumption and weekly totals
// over the fiscal year. Effective means: a project/day uses the booked actual
// hours where present, otherwise the forecast.
func BuildYearSummary(d models.Data) YearSummary {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	eff := effectiveByKey(d.Entries)

	consumed := map[string]float64{}
	forecastByP := map[string]float64{}
	actualByP := map[string]float64{}
	for k, v := range eff {
		pid := k[strings.IndexByte(k, '|')+1:]
		consumed[pid] += v
	}
	for _, e := range d.Entries {
		if entryKind(e) == models.KindActual {
			actualByP[e.ProjectID] += e.Hours
		} else {
			forecastByP[e.ProjectID] += e.Hours
		}
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
			Forecast:       round1(forecastByP[p.ID]),
			Actual:         round1(actualByP[p.ID]),
			Consumed:       round1(c),
			Remaining:      round1(rem),
			UtilizationPct: util,
		})
		ys.TotalHours += c
	}
	ys.TotalHours = round1(ys.TotalHours)

	// weekly totals over the fiscal year (effective hours)
	weeks := FYWeeks(year, startMonth)
	weekSum := make(map[int]float64)
	for k, v := range eff {
		dateStr := k[:strings.IndexByte(k, '|')]
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if w := FYWeekIndexOf(year, startMonth, t); w >= 1 {
			weekSum[w] += v
		}
	}
	for w := 1; w <= weeks; w++ {
		util := 0.0
		if d.Settings.WeeklyTargetHours > 0 {
			util = round1(weekSum[w] / d.Settings.WeeklyTargetHours * 100)
		}
		_, isoWeek := FYWeekMonday(year, startMonth, w).ISOWeek()
		hrs := round1(weekSum[w])
		ys.WeekTotals = append(ys.WeekTotals, WeekTotal{
			Week:           w,
			ISOWeek:        isoWeek,
			Label:          fmt.Sprintf("W%d · KW%02d", w, isoWeek),
			Hours:          hrs,
			TargetHours:    d.Settings.WeeklyTargetHours,
			UtilizationPct: util,
			Status:         d.Settings.ClassifyUtilization(hrs),
		})
	}
	return ys
}

// BurnPoint is a single data point of a project burn-down curve.
type BurnPoint struct {
	Week      int
	Remaining float64
}

// BuildBurndown returns the remaining-budget curve for one project over the
// fiscal year, using effective hours (actual where booked, else forecast).
func BuildBurndown(d models.Data, projectID string, budget float64) []BurnPoint {
	year := d.Settings.Year
	startMonth := d.Settings.FiscalYearStartMonth
	weeks := FYWeeks(year, startMonth)
	eff := effectiveByKey(d.Entries)
	weekSum := make(map[int]float64)
	for k, v := range eff {
		sep := strings.IndexByte(k, '|')
		if k[sep+1:] != projectID {
			continue
		}
		t, err := time.Parse("2006-01-02", k[:sep])
		if err != nil {
			continue
		}
		if w := FYWeekIndexOf(year, startMonth, t); w >= 1 {
			weekSum[w] += v
		}
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

var monthShort = []string{
	"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
	"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
}

// PeriodStat captures target vs. forecast/actual for one period (quarter/month).
type PeriodStat struct {
	Label       string
	Target      float64 // evenly split target for the period
	Forecast    float64 // planned project hours in the period
	Actual      float64 // actually booked project hours in the period
	Holiday     float64 // auto-booked holiday hours in the period (informational, not counted)
	Projected   float64 // effective project hours (past=actual, future=forecast)
	PctOfTarget float64 // projected / target * 100
}

// GoalSummary tracks fiscal-year target attainment. Only real (actual) and
// forecast project hours count towards the target. Public-holiday hours are
// reported separately but do NOT contribute to the goal.
type GoalSummary struct {
	HasTarget         bool
	StartLabel        string // FY start, e.g. 01.07.2026
	EndLabel          string // FY end, e.g. 30.06.2027
	TargetHours       float64
	ActualTotal       float64 // booked project hours (past)
	ForecastTotal     float64 // all forecast project hours
	ForecastRemaining float64 // forecast for the current and future weeks
	HolidayHours      float64 // all weekday public-holiday hours in the FY (8h each) - informational
	HolidayDays       int
	Projected         float64 // effective project hours (actual past + forecast future)
	Remaining         float64 // target - projected
	PctProjected      float64 // projected / target * 100
	PctActual         float64 // actual project hours / target * 100
	WorkingDaysYear   int
	WorkingDaysDone   int
	TargetPerWeek     float64 // target / number of FY weeks
	TargetPerMonth    float64 // target / 12
	TargetPerQuarter  float64 // target / 4
	Quarters          []PeriodStat
	Months            []PeriodStat

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

// BuildGoalSummary computes fiscal-year goal attainment. Days before the Monday
// of the current fiscal-year week count as "past" and use actual hours; the
// current and future weeks use the forecast. Public holidays on weekdays
// automatically contribute 8h towards the goal. Period targets are split evenly
// (target/4 per quarter, target/12 per month).
func BuildGoalSummary(d models.Data, cal *holidays.Calendar) GoalSummary {
	year := d.Settings.Year
	startMonth := normMonth(d.Settings.FiscalYearStartMonth)
	fy := d.CurrentFY()
	target := fy.TargetHours
	fyStart, fyEnd := FiscalYear(year, startMonth)

	now := time.Now().UTC()
	var curMonday time.Time
	switch {
	case now.Before(fyStart):
		curMonday = mondayOf(fyStart)
	case now.After(fyEnd):
		curMonday = fyEnd.AddDate(0, 0, 1)
	default:
		curMonday = mondayOf(now)
	}

	fByDate := map[string]float64{}
	aByDate := map[string]float64{}
	for _, e := range d.Entries {
		if entryKind(e) == models.KindActual {
			aByDate[e.Date] += e.Hours
		} else {
			fByDate[e.Date] += e.Hours
		}
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
		past := day.Before(curMonday)
		if weekday {
			weekdayDays++
		}
		if working {
			gs.WorkingDaysYear++
			if past {
				gs.WorkingDaysDone++
			}
		}

		f := fByDate[iso]
		a := aByDate[iso]
		work := f
		if past {
			work = a
		}
		if isHoliday {
			gs.HolidayDays++
			gs.HolidayHours += HolidayDayHours
		}

		gs.ActualTotal += a
		gs.ForecastTotal += f
		if !past {
			gs.ForecastRemaining += f
		}
		gs.Projected += work // holidays do NOT count towards the goal
		if past {
			gs.PctActual += a // accumulate hours, converted to pct later
		}

		quarters[q].Actual += a
		quarters[q].Forecast += f
		if isHoliday {
			quarters[q].Holiday += HolidayDayHours
			months[fyMonth].Holiday += HolidayDayHours
		}
		quarters[q].Projected += work
		months[fyMonth].Actual += a
		months[fyMonth].Forecast += f
		months[fyMonth].Projected += work
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
	gs.ForecastTotal = round1(gs.ForecastTotal)
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
	return gs
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
