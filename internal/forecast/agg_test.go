package forecast

import (
	"testing"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func sampleData() models.Data {
	return models.Data{
		Settings: models.Settings{Year: 2026, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 1},
		Projects: []models.Project{
			{ID: "p1", Name: "Alpha", BudgetHours: 100, Active: true},
			{ID: "p2", Name: "Beta", BudgetHours: 50, Active: true},
		},
		Entries: []models.Entry{
			// Week 3 of 2026: Mon 2026-01-12 ... Fri 2026-01-16
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8},
			{Date: "2026-01-13", ProjectID: "p1", Hours: 4},
			{Date: "2026-01-13", ProjectID: "p2", Hours: 4},
			{Date: "2026-01-14", ProjectID: "p2", Hours: 6},
		},
	}
}

func TestBuildWeekTotals(t *testing.T) {
	d := sampleData()
	cal := holidays.New(2026, "BY")
	wv := BuildWeek(d, cal, 3)

	if wv.Total != 22 {
		t.Fatalf("week total = %v, want 22", wv.Total)
	}
	if wv.ProjectTotals["p1"] != 12 {
		t.Errorf("p1 total = %v, want 12", wv.ProjectTotals["p1"])
	}
	if wv.ProjectTotals["p2"] != 10 {
		t.Errorf("p2 total = %v, want 10", wv.ProjectTotals["p2"])
	}
	if wv.UtilizationPct != 55 {
		t.Errorf("utilization = %v, want 55", wv.UtilizationPct)
	}
	if len(wv.Days) != 5 {
		t.Fatalf("days = %d, want 5", len(wv.Days))
	}
	if wv.Days[0].Date != "2026-01-12" {
		t.Errorf("first day = %s, want 2026-01-12", wv.Days[0].Date)
	}
}

func TestYearSummaryRemaining(t *testing.T) {
	d := sampleData()
	ys := BuildYearSummary(d, holidays.New(2026, "BY"))
	if ys.TotalHours != 22 {
		t.Fatalf("year total = %v, want 22", ys.TotalHours)
	}
	var alpha, beta ProjectSummary
	for _, p := range ys.Projects {
		switch p.Project.ID {
		case "p1":
			alpha = p
		case "p2":
			beta = p
		}
	}
	if alpha.Consumed != 12 || alpha.Remaining != 88 {
		t.Errorf("alpha consumed/remaining = %v/%v, want 12/88", alpha.Consumed, alpha.Remaining)
	}
	if beta.Consumed != 10 || beta.Remaining != 40 {
		t.Errorf("beta consumed/remaining = %v/%v, want 10/40", beta.Consumed, beta.Remaining)
	}
}

func TestBurndownEndsAtRemaining(t *testing.T) {
	d := sampleData()
	pts := BuildBurndown(d, "p1", "2026-01-01", "2026-12-31", 100)
	if len(pts) == 0 {
		t.Fatal("no burndown points")
	}
	last := pts[len(pts)-1]
	if last.Remaining != 88 {
		t.Errorf("final remaining = %v, want 88", last.Remaining)
	}
	if pts[0].Remaining != 100 {
		t.Errorf("start remaining = %v, want 100", pts[0].Remaining)
	}
}

func TestMondayOfISOWeek(t *testing.T) {
	m := MondayOfISOWeek(2026, 3)
	if got := m.Format("2006-01-02"); got != "2026-01-12" {
		t.Errorf("monday KW3 2026 = %s, want 2026-01-12", got)
	}
}

func TestGoalSummaryTotals(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2026, FederalState: "BY", FiscalYearTargetHours: 1000, FiscalYearStartMonth: 1},
		Entries: []models.Entry{
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8, Kind: models.KindActual},
			{Date: "2026-12-21", ProjectID: "p1", Hours: 5, Kind: models.KindForecast},
			{Date: "2026-03-02", ProjectID: "p1", Hours: 3}, // legacy entry == forecast
		},
	}
	cal := holidays.New(2026, "BY")
	gs := BuildGoalSummary(d, cal)

	if !gs.HasTarget {
		t.Fatal("expected HasTarget = true")
	}
	if gs.ActualTotal != 8 {
		t.Errorf("actual total = %v, want 8", gs.ActualTotal)
	}
	if gs.ForecastTotal != 8 {
		t.Errorf("forecast total = %v, want 8", gs.ForecastTotal)
	}
	if gs.WorkingDaysYear < 240 || gs.WorkingDaysYear > 255 {
		t.Errorf("working days = %d, out of expected 240-255 range", gs.WorkingDaysYear)
	}
	var qsum float64
	for _, q := range gs.Quarters {
		qsum += q.Target
	}
	if qsum < 995 || qsum > 1005 {
		t.Errorf("sum of quarter targets = %v, want ~1000", qsum)
	}
	if len(gs.Months) != 12 {
		t.Errorf("months = %d, want 12", len(gs.Months))
	}
}

func TestGoalHolidaysExcludedAndCapacity(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{
			Year: 2026, FederalState: "BY", FiscalYearTargetHours: 1000,
			FiscalYearStartMonth: 1, AnnualVacationDays: 10,
		},
		Entries: []models.Entry{
			// One actual booking in the past, one future forecast.
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8, Kind: models.KindActual},
			{Date: "2026-12-21", ProjectID: "p1", Hours: 5, Kind: models.KindForecast},
		},
	}
	cal := holidays.New(2026, "BY")
	gs := BuildGoalSummary(d, cal)

	// Holidays exist but must NOT contribute to the projection.
	if gs.HolidayHours <= 0 {
		t.Fatalf("expected some holiday hours, got %v", gs.HolidayHours)
	}
	wantProjected := round1(gs.ActualTotal + gs.ForecastRemaining)
	if gs.Projected != wantProjected {
		t.Errorf("projected = %v, want %v (actual + forecast remaining, no holidays)", gs.Projected, wantProjected)
	}

	// Capacity: gross weekday hours, vacation deduction.
	if gs.WeekdayHours != round1(float64(gs.WeekdayDays)*8) {
		t.Errorf("weekday hours = %v, want %v", gs.WeekdayHours, float64(gs.WeekdayDays)*8)
	}
	if gs.VacationHours != 80 {
		t.Errorf("vacation hours = %v, want 80", gs.VacationHours)
	}
	if gs.AvailableHours != round1(gs.WeekdayHours-gs.HolidayHours-gs.VacationHours) {
		t.Errorf("available hours = %v, mismatch", gs.AvailableHours)
	}
	if gs.PctOfWeekdays != round1(1000/gs.WeekdayHours*100) {
		t.Errorf("pct of weekdays = %v", gs.PctOfWeekdays)
	}

	// Pace: remaining goal divided by remaining working days.
	if gs.RemainingGoal != round1(1000-gs.ActualTotal) {
		t.Errorf("remaining goal = %v, want %v", gs.RemainingGoal, 1000-gs.ActualTotal)
	}
	if gs.RemainingWorkdays != gs.WorkingDaysYear-gs.WorkingDaysDone {
		t.Errorf("remaining workdays = %d, mismatch", gs.RemainingWorkdays)
	}
	if gs.RemainingWorkdays > 0 {
		want := round1(gs.RemainingGoal / float64(gs.RemainingWorkdays))
		if gs.RequiredPerDay != want {
			t.Errorf("required per day = %v, want %v", gs.RequiredPerDay, want)
		}
	}
}

func TestEffectiveHoursOverride(t *testing.T) {
	d := sampleData()
	// p1 on 2026-01-12 has a forecast of 8h. An actual booking of 6h on the same
	// day must OVERRIDE the forecast (effective = actual where booked).
	d.Entries = append(d.Entries, models.Entry{
		Date: "2026-01-12", ProjectID: "p1", Hours: 6, Kind: models.KindActual,
	})
	ys := BuildYearSummary(d, holidays.New(2026, "BY"))
	var alpha ProjectSummary
	for _, p := range ys.Projects {
		if p.Project.ID == "p1" {
			alpha = p
		}
	}
	// p1 effective: 6 (actual overrides 8 forecast on 01-12) + 4 (forecast 01-13) = 10
	if alpha.Consumed != 10 {
		t.Errorf("alpha effective consumed = %v, want 10 (actual overrides forecast)", alpha.Consumed)
	}
	if alpha.Forecast != 12 {
		t.Errorf("alpha forecast = %v, want 12", alpha.Forecast)
	}
	if alpha.Actual != 6 {
		t.Errorf("alpha actual = %v, want 6", alpha.Actual)
	}
}

func TestFiscalYearBoundaries(t *testing.T) {
	// FY is named after the calendar year in which it ENDS.
	cases := []struct {
		name             string
		year, startMonth int
		wantStart        string
		wantEnd          string
	}{
		// July start: FY 2027 runs 01.07.2026–30.06.2027.
		{"july-fy27", 2027, 7, "2026-07-01", "2027-06-30"},
		{"july-fy26", 2026, 7, "2025-07-01", "2026-06-30"},
		// January start equals the calendar year (no shift).
		{"jan-fy27", 2027, 1, "2027-01-01", "2027-12-31"},
		// April start: FY 2027 runs 01.04.2026–31.03.2027.
		{"april-fy27", 2027, 4, "2026-04-01", "2027-03-31"},
	}
	for _, c := range cases {
		start, end := FiscalYear(c.year, c.startMonth)
		if got := start.Format("2006-01-02"); got != c.wantStart {
			t.Errorf("%s: start = %s, want %s", c.name, got, c.wantStart)
		}
		if got := end.Format("2006-01-02"); got != c.wantEnd {
			t.Errorf("%s: end = %s, want %s", c.name, got, c.wantEnd)
		}
	}
}

func TestProjectBookable(t *testing.T) {
	p := models.Project{StartDate: "2026-03-01", EndDate: "2026-03-31"}
	cases := []struct {
		date string
		want bool
	}{
		{"2026-02-28", false},
		{"2026-03-01", true}, // inclusive start
		{"2026-03-15", true},
		{"2026-03-31", true}, // inclusive end
		{"2026-04-01", false},
	}
	for _, c := range cases {
		if got := p.Bookable(c.date); got != c.want {
			t.Errorf("Bookable(%s) = %v, want %v", c.date, got, c.want)
		}
	}
	// Open window: everything is bookable.
	open := models.Project{}
	if !open.Bookable("2026-01-01") || !open.Bookable("2026-12-31") {
		t.Errorf("open window must accept any date")
	}
}

func TestProjectWindowBurnrate(t *testing.T) {
	d := sampleData()
	d.Projects = []models.Project{
		{ID: "p1", Name: "Alpha", BudgetHours: 100, Active: true,
			StartDate: "2026-03-02", EndDate: "2026-03-06"}, // a full Mon-Fri week, no BY holidays
	}
	d.Entries = []models.Entry{
		{Date: "2026-03-03", ProjectID: "p1", Hours: 5}, // inside window
		{Date: "2026-03-10", ProjectID: "p1", Hours: 7}, // outside window
	}
	ys := BuildYearSummary(d, holidays.New(2026, "BY"))
	var p ProjectSummary
	for _, ps := range ys.Projects {
		if ps.Project.ID == "p1" {
			p = ps
		}
	}
	if !p.HasCustomWindow {
		t.Errorf("HasCustomWindow = false, want true")
	}
	if p.StartDate != "2026-03-02" || p.EndDate != "2026-03-06" {
		t.Errorf("window = %s..%s, want 2026-03-02..2026-03-06", p.StartDate, p.EndDate)
	}
	if p.WindowWorkdays != 5 {
		t.Errorf("WindowWorkdays = %d, want 5", p.WindowWorkdays)
	}
	if p.BurnPerWorkday != 20 { // 100 / 5
		t.Errorf("BurnPerWorkday = %v, want 20", p.BurnPerWorkday)
	}
	if p.BurnPerWeek != 100 { // 100 / (5/5)
		t.Errorf("BurnPerWeek = %v, want 100", p.BurnPerWeek)
	}
	if p.OutOfWindow != 7 {
		t.Errorf("OutOfWindow = %v, want 7", p.OutOfWindow)
	}
}

// vacationData has one real project and one auto-managed vacation project in
// week 3 of 2026 (Mon 2026-01-12 .. Fri 2026-01-16).
func vacationData() models.Data {
	return models.Data{
		Settings: models.Settings{
			Year: 2026, FederalState: "BY", WeeklyTargetHours: 40,
			FiscalYearStartMonth: 1, FiscalYearTargetHours: 1000,
		},
		Projects: []models.Project{
			{ID: "p1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026},
			{ID: "vacation-2026", Name: "Urlaub", BudgetHours: 240, Active: true, FiscalYear: 2026, System: models.VacationSystem},
		},
		Entries: []models.Entry{
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8, Kind: models.KindActual},
			{Date: "2026-01-13", ProjectID: "vacation-2026", Hours: 8, Kind: models.KindActual},
			{Date: "2026-01-14", ProjectID: "vacation-2026", Hours: 8, Kind: models.KindForecast},
		},
	}
}

func TestVacationExcludedFromWeek(t *testing.T) {
	d := vacationData()
	wv := BuildWeek(d, holidays.New(2026, "BY"), 3)

	// The vacation project is still displayed with its own per-project sums.
	if wv.ActualTotals["vacation-2026"] != 8 {
		t.Errorf("vacation actual total = %v, want 8 (still displayed)", wv.ActualTotals["vacation-2026"])
	}
	if wv.ProjectTotals["vacation-2026"] != 8 {
		t.Errorf("vacation forecast total = %v, want 8 (still displayed)", wv.ProjectTotals["vacation-2026"])
	}
	// ...but it is excluded from the utilization/status basis.
	if wv.EffectiveTotal != 8 {
		t.Errorf("effective total = %v, want 8 (vacation excluded)", wv.EffectiveTotal)
	}
	if wv.WorkForecast != 0 {
		t.Errorf("work forecast = %v, want 0 (only vacation was forecast)", wv.WorkForecast)
	}
	if wv.Status.Key != "min" {
		t.Errorf("status = %q, want min (8h effective work)", wv.Status.Key)
	}
}

func TestVacationExcludedFromYearAndGoal(t *testing.T) {
	d := vacationData()
	cal := holidays.New(2026, "BY")
	ys := BuildYearSummary(d, cal)

	// Vacation still appears as a project with its own consumption/budget.
	var vac ProjectSummary
	found := false
	for _, p := range ys.Projects {
		if p.Project.ID == "vacation-2026" {
			vac, found = p, true
		}
	}
	if !found {
		t.Fatal("vacation project missing from year summary")
	}
	if vac.Consumed != 16 { // 8 actual + 8 forecast effective
		t.Errorf("vacation consumed = %v, want 16", vac.Consumed)
	}
	// Weekly totals (the Ampel) exclude vacation: week 3 = only p1's 8h.
	var w3 WeekTotal
	for _, wt := range ys.WeekTotals {
		if wt.Week == 3 {
			w3 = wt
		}
	}
	if w3.Hours != 8 {
		t.Errorf("week 3 effective hours = %v, want 8 (vacation excluded)", w3.Hours)
	}

	// Goal excludes vacation entirely.
	gs := BuildGoalSummary(d, cal)
	if gs.ActualTotal != 8 {
		t.Errorf("goal actual total = %v, want 8 (vacation excluded)", gs.ActualTotal)
	}
	if gs.ForecastTotal != 0 {
		t.Errorf("goal forecast total = %v, want 0 (vacation forecast excluded)", gs.ForecastTotal)
	}
}

func TestGoalHalves(t *testing.T) {
	d := vacationData()
	gs := BuildGoalSummary(d, holidays.New(2026, "BY"))
	if len(gs.Halves) != 2 {
		t.Fatalf("halves = %d, want 2", len(gs.Halves))
	}
	if gs.Halves[0].Target != 500 || gs.Halves[1].Target != 500 {
		t.Errorf("half targets = %v/%v, want 500/500", gs.Halves[0].Target, gs.Halves[1].Target)
	}
	// Half projections sum to the overall projection (both derive from months).
	if round1(gs.Halves[0].Projected+gs.Halves[1].Projected) != gs.Projected {
		t.Errorf("halves projected sum = %v, want %v", gs.Halves[0].Projected+gs.Halves[1].Projected, gs.Projected)
	}
	if gs.Halves[0].Label == "" || gs.Halves[1].Label == "" {
		t.Error("half labels must not be empty")
	}
}
