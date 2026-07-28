package forecast

import (
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func sampleData() models.Data {
	return models.Data{
		Settings: models.Settings{Year: 2026, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 1},
		Projects: []models.Project{
			{ID: "p1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026},
			{ID: "p2", Name: "Beta", BudgetHours: 50, Active: true, FiscalYear: 2026},
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

// An assignment may run across several fiscal years. The project is re-created
// per fiscal year and carries the assignment's total budget again, so the hours
// already booked in earlier years must be deducted instead of being granted a
// second time.
func TestAssignmentCarryOverAcrossFiscalYears(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 1},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027},
			{ID: "solo", AssignmentID: "9999999", Name: "Beta", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-01-12", ProjectID: "old", Hours: 40}, // burned in the previous FY
			{Date: "2027-01-11", ProjectID: "new", Hours: 10},
			{Date: "2027-01-12", ProjectID: "solo", Hours: 10},
		},
	}
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))

	if len(ys.Projects) != 2 {
		t.Fatalf("summary covers %d projects, want 2 (only FY 2027)", len(ys.Projects))
	}
	if !ys.HasCarryOver {
		t.Error("HasCarryOver = false, want true")
	}
	if ys.TotalCarryOver != 40 {
		t.Errorf("TotalCarryOver = %v, want 40", ys.TotalCarryOver)
	}

	var cont, solo ProjectSummary
	for _, ps := range ys.Projects {
		switch ps.Project.ID {
		case "new":
			cont = ps
		case "solo":
			solo = ps
		}
	}

	if cont.CarryOver != 40 {
		t.Errorf("continued CarryOver = %v, want 40", cont.CarryOver)
	}
	if cont.AvailableBudget != 60 {
		t.Errorf("continued AvailableBudget = %v, want 60 (100 - 40)", cont.AvailableBudget)
	}
	if cont.Remaining != 50 {
		t.Errorf("continued Remaining = %v, want 50 (100 - 40 - 10)", cont.Remaining)
	}
	if cont.UtilizationPct != 50 {
		t.Errorf("continued UtilizationPct = %v, want 50", cont.UtilizationPct)
	}
	// The burn rate must be derived from the budget still available, not from
	// the full assignment budget.
	if cont.BurnPerWeek >= solo.BurnPerWeek {
		t.Errorf("continued BurnPerWeek = %v, want less than the untouched project's %v",
			cont.BurnPerWeek, solo.BurnPerWeek)
	}

	// An assignment that exists in one fiscal year only is unaffected.
	if solo.CarryOver != 0 || solo.AvailableBudget != 100 || solo.Remaining != 90 {
		t.Errorf("solo carry/available/remaining = %v/%v/%v, want 0/100/90",
			solo.CarryOver, solo.AvailableBudget, solo.Remaining)
	}
}

// Hours planned in a LATER fiscal year must not reduce the current year's
// budget: only earlier years are carried over.
func TestAssignmentCarryOverIgnoresLaterFiscalYears(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2026, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 1},
		Projects: []models.Project{
			{ID: "cur", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026},
			{ID: "next", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-01-12", ProjectID: "cur", Hours: 10},
			{Date: "2027-01-11", ProjectID: "next", Hours: 40},
		},
	}
	ys := BuildYearSummary(d, holidays.New(2026, "BY"))
	if len(ys.Projects) != 1 {
		t.Fatalf("summary covers %d projects, want 1", len(ys.Projects))
	}
	if ys.Projects[0].CarryOver != 0 {
		t.Errorf("CarryOver = %v, want 0 (later fiscal years do not count)", ys.Projects[0].CarryOver)
	}
	if ys.Projects[0].Remaining != 90 {
		t.Errorf("Remaining = %v, want 90", ys.Projects[0].Remaining)
	}
}

// Hours belong to the fiscal year their DATE falls into, not to the fiscal year
// of the project row they were booked on. With a July start, everything up to
// 30 June stays in the old FY and everything from 1 July counts towards the new
// one — even when it was entered on the previous year's project.
func TestHoursSplitAtFiscalYearBoundary(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 200, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 200, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			// FY 2026 (ends 30.06.2026) — carry-over for FY 2027.
			{Date: "2026-06-29", ProjectID: "old", Hours: 5},
			// FY 2027 (01.07.2026-30.06.2027), but entered on the OLD project row:
			// the date decides, so these hours belong to FY 2027.
			{Date: "2026-07-01", ProjectID: "old", Hours: 7},
			{Date: "2026-09-01", ProjectID: "new", Hours: 10},
			// FY 2028 — already beyond the reviewed year.
			{Date: "2027-07-05", ProjectID: "new", Hours: 3},
		},
	}
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))
	if len(ys.Projects) != 1 {
		t.Fatalf("summary covers %d projects, want 1", len(ys.Projects))
	}
	ps := ys.Projects[0]

	if ps.Consumed != 17 {
		t.Errorf("Consumed = %v, want 17 (7 booked on the old row + 10, both dated in FY 2027)", ps.Consumed)
	}
	if ps.CarryOver != 5 {
		t.Errorf("CarryOver = %v, want 5 (29.06.2026 is still FY 2026)", ps.CarryOver)
	}
	if ps.FutureFY != 3 {
		t.Errorf("FutureFY = %v, want 3 (05.07.2027 already belongs to FY 2028)", ps.FutureFY)
	}
	if ps.Remaining != 178 {
		t.Errorf("Remaining = %v, want 178 (200 - 5 - 17)", ps.Remaining)
	}
	if ys.TotalHours != 17 {
		t.Errorf("TotalHours = %v, want 17 (only hours inside the fiscal year)", ys.TotalHours)
	}
	if !ps.SpansFY || !ys.HasFYSplit {
		t.Errorf("SpansFY/HasFYSplit = %v/%v, want true/true", ps.SpansFY, ys.HasFYSplit)
	}

	want := []FYHours{
		{Year: 2026, Label: "FY 2026", Hours: 5, Past: true},
		{Year: 2027, Label: "FY 2027", Hours: 17, Current: true},
		{Year: 2028, Label: "FY 2028", Hours: 3},
	}
	if len(ps.FYSplit) != len(want) {
		t.Fatalf("FYSplit = %+v, want %d entries", ps.FYSplit, len(want))
	}
	for i, w := range want {
		if ps.FYSplit[i] != w {
			t.Errorf("FYSplit[%d] = %+v, want %+v", i, ps.FYSplit[i], w)
		}
	}
}

// The week-to-date pace counts only working days that are already over: today
// is still running and must not drag the rate down.
func TestBuildWeekToDate(t *testing.T) {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	// Put the fiscal year around today so the current week lies inside it.
	startMonth := int(now.AddDate(0, -5, 0).Month())
	year := FiscalYearOf(now, startMonth)
	cal := holidays.New(year, "BY")

	d := models.Data{
		Settings: models.Settings{Year: year, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: startMonth},
		Projects: []models.Project{{ID: "p1", Name: "Alpha", BudgetHours: 500, Active: true, FiscalYear: year}},
	}

	monday := FYWeekMonday(year, startMonth, CurrentFYWeek(year, startMonth))
	todayStr := now.Format("2006-01-02")
	wantDays, wantWorkdays := 0, 0
	for i := 0; i < 5; i++ {
		iso := monday.AddDate(0, 0, i).Format("2006-01-02")
		if cal.IsHoliday(iso) {
			continue
		}
		wantWorkdays++
		if iso >= todayStr {
			continue
		}
		wantDays++
		d.Entries = append(d.Entries, models.Entry{Date: iso, ProjectID: "p1", Hours: 8})
	}
	// Today must not count, even though hours are already planned for it.
	d.Entries = append(d.Entries, models.Entry{Date: todayStr, ProjectID: "p1", Hours: 8})

	wtd := BuildWeekToDate(d, cal)
	if wtd.WorkdaysWeek != wantWorkdays {
		t.Errorf("WorkdaysWeek = %d, want %d", wtd.WorkdaysWeek, wantWorkdays)
	}
	if wtd.ElapsedDays != wantDays {
		t.Fatalf("ElapsedDays = %d, want %d", wtd.ElapsedDays, wantDays)
	}
	if wantDays == 0 { // today is the week's first working day
		if wtd.HasData {
			t.Error("HasData = true, want false while no working day is over")
		}
		return
	}
	if !wtd.HasData {
		t.Fatal("HasData = false, want true")
	}
	if want := float64(wantDays) * 8; wtd.Hours != want {
		t.Errorf("Hours = %v, want %v (today excluded)", wtd.Hours, want)
	}
	if want := float64(wantDays) * 8; wtd.ProRataTarget != want {
		t.Errorf("ProRataTarget = %v, want %v (40h/5 per elapsed day)", wtd.ProRataTarget, want)
	}
	if wtd.RatePct != 100 {
		t.Errorf("RatePct = %v, want 100 (exactly on pace)", wtd.RatePct)
	}
	if wtd.PerDay != 8 {
		t.Errorf("PerDay = %v, want 8", wtd.PerDay)
	}
}

// A fiscal year that does not contain today has no "current week".
func TestBuildWeekToDateOutsideFiscalYear(t *testing.T) {
	year := time.Now().UTC().Year() + 5
	d := models.Data{
		Settings: models.Settings{Year: year, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
	}
	if wtd := BuildWeekToDate(d, holidays.New(year, "BY")); wtd.HasData {
		t.Errorf("HasData = true for FY %d, want false", year)
	}
}

func TestFiscalYearOf(t *testing.T) {
	cases := []struct {
		date       string
		startMonth int
		want       int
	}{
		{"2026-06-30", 7, 2026}, // last day of FY 2026
		{"2026-07-01", 7, 2027}, // first day of FY 2027
		{"2027-06-30", 7, 2027},
		{"2027-07-01", 7, 2028},
		{"2026-03-15", 1, 2026}, // start month January == calendar year
		{"2026-12-31", 1, 2026},
		{"2026-04-01", 4, 2027},
		{"2026-03-31", 4, 2026},
	}
	for _, c := range cases {
		tt, err := time.Parse("2006-01-02", c.date)
		if err != nil {
			t.Fatalf("parse %s: %v", c.date, err)
		}
		if got := FiscalYearOf(tt, c.startMonth); got != c.want {
			t.Errorf("FiscalYearOf(%s, startMonth %d) = %d, want %d", c.date, c.startMonth, got, c.want)
		}
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
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8},
			{Date: "2026-12-21", ProjectID: "p1", Hours: 5},
			{Date: "2026-03-02", ProjectID: "p1", Hours: 3},
		},
	}
	cal := holidays.New(2026, "BY")
	gs := BuildGoalSummary(d, cal)

	if !gs.HasTarget {
		t.Fatal("expected HasTarget = true")
	}
	// All hours count towards the projection regardless of past/future.
	if gs.Projected != 16 {
		t.Errorf("projected = %v, want 16 (8+5+3)", gs.Projected)
	}
	// Booked (past) + forecast (today/future) must add up to the projection.
	if round1(gs.ActualTotal+gs.ForecastRemaining) != gs.Projected {
		t.Errorf("actual %v + forecast %v != projected %v", gs.ActualTotal, gs.ForecastRemaining, gs.Projected)
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
			// One booking in the past, one in the future.
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8},
			{Date: "2026-12-21", ProjectID: "p1", Hours: 5},
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

func TestForecastActualSplitByDate(t *testing.T) {
	// Hours are classified as booked or forecast purely by date: days before
	// today are booked ("Actual"), today and later are forecast.
	now := time.Now().UTC()
	year := now.Year()
	past := now.AddDate(0, 0, -14).Format("2006-01-02")
	future := now.AddDate(0, 0, 14).Format("2006-01-02")
	d := models.Data{
		Settings: models.Settings{Year: year, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 1},
		Projects: []models.Project{{ID: "p1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: year}},
		Entries: []models.Entry{
			{Date: past, ProjectID: "p1", Hours: 8},
			{Date: future, ProjectID: "p1", Hours: 5},
		},
	}
	ys := BuildYearSummary(d, holidays.New(year, "BY"))
	var alpha ProjectSummary
	for _, p := range ys.Projects {
		if p.Project.ID == "p1" {
			alpha = p
		}
	}
	if alpha.Actual != 8 {
		t.Errorf("alpha actual (past) = %v, want 8", alpha.Actual)
	}
	if alpha.Forecast != 5 {
		t.Errorf("alpha forecast (future) = %v, want 5", alpha.Forecast)
	}
	if alpha.Consumed != 13 {
		t.Errorf("alpha consumed (all) = %v, want 13", alpha.Consumed)
	}
	if alpha.Remaining != 87 {
		t.Errorf("alpha remaining = %v, want 87", alpha.Remaining)
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
		{ID: "p1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026,
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
			{Date: "2026-01-12", ProjectID: "p1", Hours: 8},
			{Date: "2026-01-13", ProjectID: "vacation-2026", Hours: 8},
			{Date: "2026-01-14", ProjectID: "vacation-2026", Hours: 8},
		},
	}
}

func TestVacationCountsTowardsUtilization(t *testing.T) {
	d := vacationData()
	wv := BuildWeek(d, holidays.New(2026, "BY"), 3)

	// The vacation project is displayed with its own per-project sum.
	if wv.ProjectTotals["vacation-2026"] != 16 {
		t.Errorf("vacation total = %v, want 16 (still displayed)", wv.ProjectTotals["vacation-2026"])
	}
	if wv.ProjectTotals["p1"] != 8 {
		t.Errorf("p1 total = %v, want 8", wv.ProjectTotals["p1"])
	}
	// Vacation consumes available working time, so it counts towards the
	// utilization/status basis.
	if wv.Total != 24 {
		t.Errorf("week total = %v, want 24 (vacation included)", wv.Total)
	}
	if wv.Status.Key != "min" {
		t.Errorf("status = %q, want min (24h <= 26h minimum)", wv.Status.Key)
	}
}

func TestVacationExcludedFromGoalOnly(t *testing.T) {
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
	if vac.Consumed != 16 { // 8 + 8, both days count
		t.Errorf("vacation consumed = %v, want 16", vac.Consumed)
	}
	// Weekly totals (the Ampel) include vacation: week 3 = 8h p1 + 16h vacation.
	var w3 WeekTotal
	for _, wt := range ys.WeekTotals {
		if wt.Week == 3 {
			w3 = wt
		}
	}
	if w3.Hours != 24 {
		t.Errorf("week 3 hours = %v, want 24 (vacation included)", w3.Hours)
	}

	// The FY goal still excludes vacation entirely.
	gs := BuildGoalSummary(d, cal)
	if gs.ActualTotal != 8 {
		t.Errorf("goal actual total = %v, want 8 (vacation excluded)", gs.ActualTotal)
	}
	if gs.ForecastRemaining != 0 {
		t.Errorf("goal forecast remaining = %v, want 0 (vacation excluded, no future work)", gs.ForecastRemaining)
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

func TestSankeySpanUnits(t *testing.T) {
	maxW := FYWeeks(2026, 1)
	cases := []struct {
		key   string
		unit  string
		weeks int
	}{
		{"1w", "week", 1},
		{"2w", "week", 2},
		{"4w", "week", 4},
		{"2m", "week", 8},
		{"3m", "month", 13},
		{"fy", "month", maxW},
	}
	for _, c := range cases {
		_, weeks, unit := sankeySpan(2026, 1, 10, c.key)
		if unit != c.unit || weeks != c.weeks {
			t.Errorf("%s: weeks=%d unit=%s, want weeks=%d unit=%s", c.key, weeks, unit, c.weeks, c.unit)
		}
	}
	// An unknown key falls back to the default range.
	if NormalizeSankeyRange("bogus") != SankeyDefaultRange {
		t.Errorf("unknown range not normalised to default")
	}
}

func TestBuildSankeyFiscalYearSeparatesVacation(t *testing.T) {
	d := vacationData() // FY 2026 (calendar year), all entries in January
	sk := BuildSankey(d, holidays.New(2026, "BY"), "fy", 0)

	if sk.Unit != "month" {
		t.Fatalf("unit = %q, want month", sk.Unit)
	}
	// Vacation hours (16h) are not part of the bands; only p1's 8h remain.
	if sk.Total != 8 {
		t.Errorf("total = %v, want 8 (vacation not in the bands)", sk.Total)
	}
	if _, ok := sk.ProjectTotals["vacation-2026"]; ok {
		t.Errorf("vacation must not appear as a Sankey band")
	}
	if sk.VacationTotal != 16 {
		t.Errorf("vacation total = %v, want 16", sk.VacationTotal)
	}
	if sk.ProjectTotals["p1"] != 8 {
		t.Errorf("p1 total = %v, want 8", sk.ProjectTotals["p1"])
	}
	if len(sk.Projects) != 1 || sk.Projects[0].ID != "p1" {
		t.Fatalf("projects = %+v, want only p1", sk.Projects)
	}
	if sk.MaxBucket != 8 {
		t.Errorf("max bucket = %v, want 8", sk.MaxBucket)
	}
	// The January column carries the hours; every FY month is a bucket.
	var jan *SankeyBucket
	for i := range sk.Buckets {
		if sk.Buckets[i].Label == "Jan" {
			jan = &sk.Buckets[i]
		}
	}
	if jan == nil || jan.Total != 8 {
		t.Fatalf("january bucket = %+v, want total 8", jan)
	}
	if jan.VacationHours != 16 || jan.VacationDays != 2 {
		t.Errorf("january vacation = %v h / %v days, want 16 h / 2 days", jan.VacationHours, jan.VacationDays)
	}
	// Free time = weekdays*8 - holidays - vacation - planned hours.
	wantFree := jan.WeekdayHours - jan.HolidayHours - jan.VacationHours - jan.Total
	if jan.FreeHours != round1(wantFree) {
		t.Errorf("january free = %v, want %v", jan.FreeHours, round1(wantFree))
	}
	if jan.WeekdayHours == 0 {
		t.Error("weekday hours must be counted for the capacity")
	}
	if len(sk.Buckets) != 12 {
		t.Errorf("fy buckets = %d, want 12 months", len(sk.Buckets))
	}
	// A whole-FY span cannot be shifted any further.
	if sk.CanPrev || sk.CanNext {
		t.Error("fy span must not offer prev/next navigation")
	}
}

func TestShiftSankeySpan(t *testing.T) {
	const maxW = 52
	cases := []struct {
		name                      string
		base, weeks, offset       int
		wantStart, wantAppliedOff int
	}{
		{"no shift", 20, 4, 0, 20, 0},
		{"one span back", 20, 4, -1, 16, -1},
		{"two spans forward", 20, 4, 2, 28, 2},
		{"clamped at fy start", 3, 4, -5, 1, -1},
		{"clamped at fy end", 48, 4, 3, 49, 1},
		{"whole fy cannot move", 1, maxW, -2, 1, 0},
	}
	for _, c := range cases {
		start, applied := shiftSankeySpan(c.base, c.weeks, maxW, c.offset)
		if start != c.wantStart || applied != c.wantAppliedOff {
			t.Errorf("%s: start=%d offset=%d, want start=%d offset=%d", c.name, start, applied, c.wantStart, c.wantAppliedOff)
		}
		if start < 1 || start+c.weeks-1 > maxW {
			t.Errorf("%s: span %d..%d leaves the fiscal year", c.name, start, start+c.weeks-1)
		}
	}
}

func TestBuildSankeyOffsetShiftsIntoThePast(t *testing.T) {
	d := vacationData()
	cal := holidays.New(2026, "BY")
	cur := BuildSankey(d, cal, "4w", 0)
	prev := BuildSankey(d, cal, "4w", -1)

	if !cur.CanPrev {
		t.Fatal("a 4-week span inside the FY must allow stepping back")
	}
	if prev.Offset != -1 {
		t.Errorf("offset = %d, want -1", prev.Offset)
	}
	if len(prev.Buckets) != len(cur.Buckets) {
		t.Errorf("buckets = %d, want %d (same span length)", len(prev.Buckets), len(cur.Buckets))
	}
	if prev.RangeLabel == cur.RangeLabel {
		t.Errorf("shifted span must cover a different range (got %q twice)", prev.RangeLabel)
	}
	if prev.PrevOffset != -2 || prev.NextOffset != 0 {
		t.Errorf("navigation offsets = %d/%d, want -2/0", prev.PrevOffset, prev.NextOffset)
	}
}
