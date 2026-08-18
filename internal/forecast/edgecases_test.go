package forecast

import (
	"testing"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// These cases pin down the behaviour for documents that are structurally odd
// but reachable through the UI. They are the ones where a silent miscalculation
// would be hardest to notice.

// Two rows of the same fiscal year sharing an assignment ID pool their hours.
// Each row then reports the pooled amount, so the fiscal-year roll-up must
// still count those hours exactly once instead of twice.
func TestDuplicateAssignmentIsNotCountedTwice(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "a", AssignmentID: "4711", Name: "Phase 1", BudgetHours: 100, Active: true, FiscalYear: 2027},
			{ID: "b", AssignmentID: "4711", Name: "Phase 2", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-09-01", ProjectID: "a", Hours: 10},
			{Date: "2026-09-02", ProjectID: "b", Hours: 10},
		},
	}
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))
	if ys.TotalHours != 20 {
		t.Errorf("TotalHours = %v, want 20 (the assignment's hours, counted once)", ys.TotalHours)
	}
	if ys.TotalCarryOver != 0 {
		t.Errorf("TotalCarryOver = %v, want 0", ys.TotalCarryOver)
	}
	// The goal page counts by date and knows nothing about the pooling, so it is
	// the reference the dashboard has to match.
	gs := BuildGoalSummary(d, holidays.New(2027, "BY"))
	if gs.Projected != ys.TotalHours {
		t.Errorf("goal %v h != dashboard %v h", gs.Projected, ys.TotalHours)
	}
}

// The burn-down of a continued assignment must end on the same remaining hours
// the project table shows, even when part of this year's hours were booked on
// the previous year's project row.
func TestBurndownMatchesRemaining(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-06-30", ProjectID: "old", Hours: 20}, // FY 2026 -> carry-over
			{Date: "2026-07-01", ProjectID: "old", Hours: 10}, // FY 2027, still on the old row
			{Date: "2026-09-15", ProjectID: "new", Hours: 30}, // FY 2027, on the new row
		},
	}
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))
	ps := ys.Projects[0]
	if ps.Consumed != 40 || ps.Remaining != 340 {
		t.Fatalf("consumed/remaining = %v/%v, want 40/340", ps.Consumed, ps.Remaining)
	}

	pts := BuildBurndown(d, ps.Project.ID, ps.StartDate, ps.EndDate, ps.AvailableBudget)
	if len(pts) == 0 {
		t.Fatal("no burndown points")
	}
	if got := pts[len(pts)-1].Remaining; got != ps.Remaining {
		t.Errorf("burndown ends at %v, but the table says %v h remaining", got, ps.Remaining)
	}
}

// An assignment whose hours are dated in the reviewed fiscal year but that has
// no project row there is invisible in the project table. The goal counts those
// hours by date, so the two views must not silently disagree.
func TestHoursWithoutAProjectRowInTheReviewedYear(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		FiscalYears: map[int]models.FiscalYearSettings{
			2027: {VacationDays: 0, HolidayDays: intPtr(0), WeekdayHours: 1000},
		},
		Projects: []models.Project{
			// Only an FY 2026 row exists, but hours are dated in FY 2027.
			{ID: "old", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2026},
		},
		Entries: []models.Entry{
			{Date: "2026-07-01", ProjectID: "old", Hours: 12},
		},
	}
	cal := holidays.New(2027, "BY")
	gs := BuildGoalSummary(d, cal)
	ys := BuildYearSummary(d, cal)

	if gs.Projected != 12 {
		t.Errorf("goal projection = %v, want 12 (hours belong to the FY of their date)", gs.Projected)
	}
	var weekSum float64
	for _, wt := range ys.WeekTotals {
		weekSum += wt.Hours
	}
	if weekSum != 12 {
		t.Errorf("weekly totals = %v, want 12", weekSum)
	}
	// Documented consequence: without a row for the assignment there is nothing
	// to attribute a budget to, so the project roll-up stays empty.
	if len(ys.Projects) != 0 || ys.TotalHours != 0 {
		t.Errorf("project roll-up = %d rows / %v h, want 0/0", len(ys.Projects), ys.TotalHours)
	}
}

// A week the user never touched must read as an empty week, not as a gap: the
// weekly table lists every week up to the last planned one.
func TestWeekTotalsCoverGapsUpToTheLastPlannedWeek(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-07-01", ProjectID: "p1", Hours: 8},  // week 1
			{Date: "2026-07-22", ProjectID: "p1", Hours: 8},  // week 4
			{Date: "2026-08-05", ProjectID: "p1", Hours: 40}, // week 6, overbooked
		},
	}
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))
	if ys.LastPlannedWeek != 6 {
		t.Fatalf("LastPlannedWeek = %d, want 6", ys.LastPlannedWeek)
	}
	byWeek := map[int]WeekTotal{}
	for _, wt := range ys.WeekTotals {
		byWeek[wt.Week] = wt
	}
	for _, w := range []int{2, 3, 5} {
		if byWeek[w].Hours != 0 {
			t.Errorf("week %d = %v h, want an empty week", w, byWeek[w].Hours)
		}
		if byWeek[w].Status.Key != "min" {
			t.Errorf("week %d status = %q, want min", w, byWeek[w].Status.Key)
		}
	}
	if byWeek[6].Status.Key != "optimal" {
		t.Errorf("week 6 (40 h) status = %q, want optimal", byWeek[6].Status.Key)
	}
	if byWeek[6].UtilizationPct != 100 {
		t.Errorf("week 6 utilization = %v, want 100", byWeek[6].UtilizationPct)
	}
}

// Hours booked outside a project's window are flagged but still count: the
// window is a hint, never a hard boundary.
func TestOutOfWindowHoursAreFlaggedButCounted(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{
				ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027,
				StartDate: "2026-09-01", EndDate: "2026-09-30",
			},
		},
		Entries: []models.Entry{
			{Date: "2026-08-31", ProjectID: "p1", Hours: 4}, // before the window
			{Date: "2026-09-15", ProjectID: "p1", Hours: 6}, // inside
			{Date: "2026-10-01", ProjectID: "p1", Hours: 5}, // after the window
		},
	}
	ps := BuildYearSummary(d, holidays.New(2027, "BY")).Projects[0]
	if ps.Consumed != 15 {
		t.Errorf("consumed = %v, want 15 (out-of-window hours count too)", ps.Consumed)
	}
	if ps.OutOfWindow != 9 {
		t.Errorf("out of window = %v, want 9", ps.OutOfWindow)
	}
	if ps.StartDate != "2026-09-01" || ps.EndDate != "2026-09-30" {
		t.Errorf("window = %s..%s, want the configured one", ps.StartDate, ps.EndDate)
	}
	if !ps.HasCustomWindow {
		t.Error("HasCustomWindow = false, want true")
	}
	// A window reaching beyond the fiscal year is clamped to it.
	d.Projects[0].StartDate = "2020-01-01"
	d.Projects[0].EndDate = "2030-12-31"
	ps = BuildYearSummary(d, holidays.New(2027, "BY")).Projects[0]
	if ps.StartDate != "2026-07-01" || ps.EndDate != "2027-06-30" {
		t.Errorf("clamped window = %s..%s, want the fiscal year", ps.StartDate, ps.EndDate)
	}
}

// A vacation project must keep its own row and its derived budget, but never
// leak into the fiscal-year roll-ups or the goal.
func TestVacationStaysOutOfTheRollUps(t *testing.T) {
	year := 2027
	d := models.Data{
		Settings: models.Settings{Year: year, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		FiscalYears: map[int]models.FiscalYearSettings{
			year: {VacationDays: 30, HolidayDays: intPtr(0), WeekdayHours: 1000},
		},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: year},
			{
				ID: models.VacationProjectID(year), Name: "Urlaub", BudgetHours: 240,
				Color: models.VacationColor, Active: true, FiscalYear: year, System: models.VacationSystem,
			},
		},
		Entries: []models.Entry{
			{Date: "2026-09-01", ProjectID: "p1", Hours: 8},
			{Date: "2026-09-02", ProjectID: models.VacationProjectID(year), Hours: 8},
		},
	}
	cal := holidays.New(year, "BY")
	ys := BuildYearSummary(d, cal)
	gs := BuildGoalSummary(d, cal)

	if len(ys.Projects) != 2 {
		t.Fatalf("%d project rows, want 2 (vacation keeps its own row)", len(ys.Projects))
	}
	if ys.TotalHours != 8 || ys.TotalBudget != 100 {
		t.Errorf("roll-up = %v h / %v h budget, want 8/100 (vacation excluded)", ys.TotalHours, ys.TotalBudget)
	}
	if gs.Projected != 8 {
		t.Errorf("goal projection = %v, want 8", gs.Projected)
	}
	// The week grid does count vacation: it consumes working time.
	var weekSum float64
	for _, wt := range ys.WeekTotals {
		weekSum += wt.Hours
	}
	if weekSum != 16 {
		t.Errorf("weekly totals = %v, want 16 (vacation included)", weekSum)
	}
	// The capacity already deducts the vacation days, so counting the hours
	// again would deduct them twice.
	if c := BuildFYCapacity(d, cal, year); c.RemainingHours != 1000-240 {
		t.Errorf("capacity = %v, want 760", c.RemainingHours)
	}
}
