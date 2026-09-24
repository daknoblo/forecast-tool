package forecast

import (
	"testing"

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

func TestMonthlyAndWeeklyTotalsExcludeOtherFiscalYears(t *testing.T) {
	d := boundaryData()
	d.Entries = append(d.Entries,
		models.Entry{Date: "2027-06-30", ProjectID: "new", Hours: 5},
		models.Entry{Date: "2027-07-01", ProjectID: "new", Hours: 7},
	)
	cal := holidays.New(2027, "BY")
	ys := BuildYearSummary(d, cal)
	for _, tc := range []struct {
		month, monday string
		index         int
		hours         float64
	}{
		{"2026-07-01", "2026-06-29", 0, 6},
		{"2027-06-01", "2027-06-28", len(ys.WeekTotals) - 1, 5},
	} {
		plan := BuildStoredMonthPlan(d, cal, monthTestDate(tc.month), monthTestDate(tc.month))
		w := monthTestWeek(t, plan, tc.monday)
		if w.Stored != tc.hours || ys.WeekTotals[tc.index].Hours != tc.hours {
			t.Fatalf("%s: calendar=%g, weekly=%g, want %g", tc.month, w.Stored, ys.WeekTotals[tc.index].Hours, tc.hours)
		}
		sankey := BuildSankey(d, cal, "1w", tc.index+1-CurrentFYWeek(2027, 7))
		if len(sankey.Buckets) != 1 || sankey.Buckets[0].Total != tc.hours {
			t.Fatalf("%s: Sankey does not match the calendar and weekly total: %+v", tc.month, sankey.Buckets)
		}
		if ys.WeekTotals[tc.index].Month != tc.month[:7] {
			t.Fatalf("week links outside the FY: %+v", ys.WeekTotals[tc.index])
		}
		for _, day := range w.Days {
			if !day.InYear && (day.Stored != 0 || day.Capacity != 0 || len(day.Events) != 0) {
				t.Fatalf("out-of-FY day counted: %+v", day)
			}
		}
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
