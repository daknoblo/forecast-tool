package forecast

import (
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// boundaryData puts one day of hours on each side of the fiscal-year border.
// FY 2027 runs 01.07.2026-30.06.2027; 01.07.2026 is a Wednesday, so the first
// FY week starts on Monday 29.06.2026 - still inside FY 2026.
func boundaryData() models.Data {
	return models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		FiscalYears: map[int]models.FiscalYearSettings{
			2027: {VacationDays: 0, HolidayDays: intPtr(0), WeekdayHours: 1000},
		},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "4711", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "4711", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-06-29", ProjectID: "old", Hours: 8}, // Monday, still FY 2026
			{Date: "2026-06-30", ProjectID: "old", Hours: 8}, // Tuesday, still FY 2026
			{Date: "2026-07-01", ProjectID: "new", Hours: 6}, // Wednesday, first day of FY 2027
		},
	}
}

// The first fiscal-year week reaches back into the previous fiscal year. Those
// days are shown so they can still be edited, but their hours belong to the old
// FY - the week total, the utilization and the holiday hours must not count
// them, otherwise the grid and the dashboard report different weeks.
func TestFirstWeekExcludesPreviousFiscalYearHours(t *testing.T) {
	d := boundaryData()
	cal := holidays.New(2027, "BY")

	monday := FYWeekMonday(2027, 7, 1)
	if got := monday.Format("2006-01-02"); got != "2026-06-29" {
		t.Fatalf("week 1 starts on %s, want 2026-06-29", got)
	}

	wv := BuildWeek(d, cal, 1)
	if wv.Total != 6 {
		t.Errorf("week grid total = %v, want 6 (only 01.07. lies in FY 2027)", wv.Total)
	}
	if wv.ProjectTotals["old"] != 0 {
		t.Errorf("week grid counts %v h of the previous FY for the old row", wv.ProjectTotals["old"])
	}
	if wv.UtilizationPct != 15 {
		t.Errorf("utilization = %v %%, want 15 (6 of 40 h)", wv.UtilizationPct)
	}
	// The out-of-year days stay visible and keep their hours so they remain
	// editable; only the roll-up ignores them.
	if len(wv.Days) != 5 || wv.Days[0].InYear {
		t.Fatalf("day 0 = %+v, want a visible out-of-year day", wv.Days[0])
	}
	if wv.Days[0].Hours["old"] != 8 {
		t.Errorf("out-of-year cell lost its hours: %+v", wv.Days[0].Hours)
	}

	// The dashboard's weekly table is the reference the grid has to match.
	ys := BuildYearSummary(d, cal)
	if len(ys.WeekTotals) == 0 {
		t.Fatal("no weekly totals")
	}
	if ys.WeekTotals[0].Hours != wv.Total {
		t.Errorf("dashboard week 1 = %v h, grid = %v h", ys.WeekTotals[0].Hours, wv.Total)
	}
	// So is the dashboard sankey.
	data := BuildSankey(d, cal, "1w", -CurrentFYWeek(2027, 7)+1)
	if len(data.Buckets) > 0 && data.Buckets[0].Total != wv.Total {
		t.Errorf("sankey week 1 = %v h, grid = %v h", data.Buckets[0].Total, wv.Total)
	}
}

// A span starting on week 1 must not pick the previous year's hours up either.
func TestSpanExcludesPreviousFiscalYearHours(t *testing.T) {
	d := boundaryData()
	sv := BuildSpan(d, holidays.New(2027, "BY"), 1, 2)
	if sv.Total != 6 {
		t.Errorf("span total = %v, want 6", sv.Total)
	}
	if sv.ProjectTotals["old"] != 0 {
		t.Errorf("span counts %v h of the previous FY", sv.ProjectTotals["old"])
	}
}

// The hours of the previous fiscal year are not lost: they reduce the budget
// that is still available in this one.
func TestPreviousYearHoursReduceTheAvailableBudget(t *testing.T) {
	d := boundaryData()
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))
	if len(ys.Projects) != 1 {
		t.Fatalf("%d project rows, want 1", len(ys.Projects))
	}
	ps := ys.Projects[0]
	if ps.CarryOver != 16 {
		t.Errorf("carry-over = %v, want 16 (29.06. + 30.06.)", ps.CarryOver)
	}
	if ps.Consumed != 6 {
		t.Errorf("consumed = %v, want 6", ps.Consumed)
	}
	if ps.AvailableBudget != 84 {
		t.Errorf("available = %v, want 84 (100 - 16)", ps.AvailableBudget)
	}
	if ys.TotalAvailable != 84 {
		t.Errorf("TotalAvailable = %v, want 84", ys.TotalAvailable)
	}
	if ys.TotalHours != 6 {
		t.Errorf("TotalHours = %v, want 6", ys.TotalHours)
	}
	// Booked + forecast + not yet planned has to be the available budget.
	if got := ys.TotalActual + ys.TotalForecast + ys.TotalRemaining; got != ys.TotalAvailable {
		t.Errorf("%v + %v + %v = %v, want %v",
			ys.TotalActual, ys.TotalForecast, ys.TotalRemaining, got, ys.TotalAvailable)
	}
	// The goal counts by date, so it must not see the previous year either.
	if gs := BuildGoalSummary(d, holidays.New(2027, "BY")); gs.Projected != 6 {
		t.Errorf("goal projection = %v, want 6", gs.Projected)
	}
}

// The same has to hold on the closing edge: the last FY week can run into the
// next fiscal year.
func TestLastWeekExcludesNextFiscalYearHours(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2027-06-30", ProjectID: "p1", Hours: 5}, // last day of FY 2027
			{Date: "2027-07-01", ProjectID: "p1", Hours: 7}, // already FY 2028
		},
	}
	cal := holidays.New(2027, "BY")
	last := FYWeeks(2027, 7)
	wv := BuildWeek(d, cal, last)

	var inWeek bool
	for _, c := range wv.Days {
		if c.Date == "2027-07-01" {
			inWeek = true
		}
	}
	if !inWeek {
		t.Skip("the last FY week does not reach into the next fiscal year this time")
	}
	if wv.Total != 5 {
		t.Errorf("last week total = %v, want 5 (01.07. already belongs to FY 2028)", wv.Total)
	}
}

// Public holidays outside the fiscal year must not inflate the week's holiday
// hours either - the capacity calculation only counts in-FY holidays.
func TestOutOfYearHolidaysDoNotCount(t *testing.T) {
	// FY 2026 starts 01.01.2026 (January start), so week 1 begins on 29.12.2025
	// and contains New Year's Day 01.01.2026 plus 31.12.2025, which is not one.
	d := models.Data{
		Settings: models.Settings{Year: 2026, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 1},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2026},
		},
	}
	cal := holidays.New(2026, "BY")
	wv := BuildWeek(d, cal, 1)

	fyStart, _ := FiscalYear(2026, 1)
	var want float64
	for _, c := range wv.Days {
		day, err := time.Parse("2006-01-02", c.Date)
		if err != nil {
			t.Fatalf("parse %s: %v", c.Date, err)
		}
		if c.IsHoliday && !day.Before(fyStart) {
			want += HolidayDayHours
		}
	}
	if wv.HolidayHours != want {
		t.Errorf("holiday hours = %v, want %v (only in-FY holidays)", wv.HolidayHours, want)
	}
}
