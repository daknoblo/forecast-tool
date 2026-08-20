package sample

import (
	"reflect"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// testDay sits in the middle of the first half of fiscal year 2027.
var testDay = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

func build(t *testing.T, now time.Time) models.Data {
	t.Helper()
	base := models.DefaultData(forecast.FiscalYearOf(now, models.DefaultFiscalYearStartMonth))
	return Data(now, base)
}

// splitByDay reports the hours dated before and from `day` on. The aggregation
// package classifies booked vs. forecast against the real current date, which a
// test cannot pin down, so the split is done here against the generated day.
func splitByDay(d models.Data, day time.Time) (booked, forecast float64) {
	iso := day.Format("2006-01-02")
	for _, e := range d.Entries {
		if e.Date < iso {
			booked += e.Hours
			continue
		}
		forecast += e.Hours
	}
	return booked, forecast
}

func TestDataIsValidDeterministicAndLeavesTheBaseAlone(t *testing.T) {
	base := models.DefaultData(forecast.FiscalYearOf(testDay, models.DefaultFiscalYearStartMonth))
	base.Projects = []models.Project{{
		ID: "real", AssignmentID: "1", Name: "Echt", BudgetHours: 10,
		Color: "#2563eb", Active: true, FiscalYear: base.Settings.Year,
	}}
	base.Entries = []models.Entry{{Date: "2026-10-15", ProjectID: "real", Hours: 8}}

	d := Data(testDay, base)
	if err := models.Validate(d); err != nil {
		t.Fatalf("sample document is invalid: %v", err)
	}
	if len(base.Projects) != 1 || len(base.Entries) != 1 {
		t.Error("Data modified the document it was built from")
	}
	for _, p := range d.Projects {
		if p.Name == "Echt" {
			t.Error("sample document still contains a real project")
		}
	}
	if !reflect.DeepEqual(d, Data(testDay, base)) {
		t.Error("two calls for the same day produce different documents")
	}
}

func TestSampleFillsEveryView(t *testing.T) {
	d := build(t, testDay)
	cal := holidays.Get(d.Settings.Year, d.Settings.FederalState)
	ys := forecast.BuildYearSummary(d, cal)

	active, inactive, vacation := 0, 0, 0
	over, under := 0, 0
	for _, ps := range ys.Projects {
		switch {
		case ps.Project.IsVacation():
			vacation++
		case ps.Project.Active:
			active++
		default:
			inactive++
		}
		if ps.Project.IsVacation() || !ps.Project.Active {
			continue
		}
		if ps.Remaining < 0 {
			over++
		} else {
			under++
		}
	}
	if active != 5 || inactive != 2 || vacation != 1 {
		t.Errorf("projects: %d active, %d inactive, %d vacation; want 5/2/1", active, inactive, vacation)
	}
	// Both directions have to occur, otherwise the remaining-budget indicators
	// would only ever show one colour.
	if over == 0 || under == 0 {
		t.Errorf("%d projects over and %d under budget; want both", over, under)
	}
	if !ys.HasCarryOver || !ys.HasReleased {
		t.Errorf("carry-over = %v, released = %v; want both", ys.HasCarryOver, ys.HasReleased)
	}
	if booked, fc := splitByDay(d, testDay); booked <= 0 || fc <= 0 {
		t.Errorf("booked = %v, forecast = %v; want hours on both sides of today", booked, fc)
	}

	// Every traffic-light tier has to occur, so the weekly utilization shows all
	// of its states.
	tiers := map[string]bool{}
	for _, wt := range ys.WeekTotals {
		if wt.Hours > 0 {
			tiers[d.Settings.ClassifyUtilization(wt.Hours).Key] = true
		}
	}
	for _, key := range []string{"min", "optimal", "high", "over"} {
		if !tiers[key] {
			t.Errorf("no week classified as %q", key)
		}
	}

	// The goal page needs a target and a projection to draw anything.
	gs := forecast.BuildGoalSummary(d, cal)
	if gs.TargetHours <= 0 || gs.Projected <= 0 {
		t.Errorf("goal target = %v, projected = %v; want both > 0", gs.TargetHours, gs.Projected)
	}

	vacationHours := 0.0
	for _, e := range d.Entries {
		if e.ProjectID == models.VacationProjectID(d.Settings.Year) {
			vacationHours += e.Hours
		}
	}
	if vacationHours <= 0 {
		t.Error("no vacation is planned")
	}
}

// The sample is anchored on the active fiscal year, which is not necessarily the
// one today falls into. Every year still has to come out populated.
func TestSampleFillsAnyFiscalYear(t *testing.T) {
	for _, offset := range []int{-2, -1, 0, 1, 2} {
		base := models.DefaultData(forecast.FiscalYearOf(testDay, models.DefaultFiscalYearStartMonth) + offset)
		d := Data(testDay, base)
		if err := models.Validate(d); err != nil {
			t.Fatalf("offset %d: invalid document: %v", offset, err)
		}
		ys := forecast.BuildYearSummary(d, holidays.Get(d.Settings.Year, d.Settings.FederalState))
		if ys.TotalHours <= 0 {
			t.Errorf("offset %d: fiscal year has no hours", offset)
		}
	}
}

// Inactive projects are hidden from the forecast grid, so hours planned on them
// would make the grid rows and the day totals disagree.
func TestInactiveProjectsCarryNoForecast(t *testing.T) {
	d := build(t, testDay)
	inactive := map[string]string{}
	for _, p := range d.Projects {
		if !p.Active && p.FiscalYear == d.Settings.Year {
			inactive[p.ID] = p.Name
		}
	}
	iso := testDay.Format("2006-01-02")
	for _, e := range d.Entries {
		if name, ok := inactive[e.ProjectID]; ok && e.Date >= iso {
			t.Errorf("inactive project %q carries %v forecast hours on %s", name, e.Hours, e.Date)
		}
	}
}
