package forecast

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func monthTestDate(iso string) time.Time {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		panic(err)
	}
	return t
}

func monthTestData() models.Data {
	d := models.DefaultData(2026)
	d.Settings.FiscalYearStartMonth = 1
	d.Settings.FederalState = "SN"
	d.Projects = []models.Project{
		{ID: "p", AssignmentID: "123", Name: "Project", Active: true, FiscalYear: 2026},
		{ID: "v", Name: "Urlaub", System: models.VacationSystem, FiscalYear: 2026},
	}
	d.Entries = nil
	return d
}

func TestMonthWeeklyProjectTotals(t *testing.T) {
	d := monthTestData()
	d.Settings.Year = 2027
	d.Settings.FiscalYearStartMonth = 7
	d.Projects[0].FiscalYear = 2027
	d.Projects[0].Color = "#123456"
	d.Projects[1].FiscalYear = 2027
	d.Projects = append(d.Projects, models.Project{
		ID: "q", Name: "Inactive", Color: "#654321", FiscalYear: 2027,
	})
	d.Entries = []models.Entry{
		{Date: "2026-06-29", ProjectID: "p", Hours: 99},
		{Date: "2026-07-01", ProjectID: "p", Hours: 8},
		{Date: "2026-07-02", ProjectID: "p", Hours: 6},
		{Date: "2026-07-04", ProjectID: "p", Hours: 2},
		{Date: "2026-07-03", ProjectID: "q", Hours: 10},
		{Date: "2026-07-03", ProjectID: "v", Hours: 8},
		{Date: "2026-07-06", ProjectID: "q", Hours: 60},
	}
	now := monthTestDate("2026-07-02")
	for _, estimate := range []bool{false, true} {
		plan := buildMonthPlan(d, holidays.Get(2027, "SN"), now, now, estimate)
		week := plan.Weeks[0]
		if week.FYWeek != 1 || week.Capacity != 16 || week.Stored != 26 || week.Vacation != 8 || week.Absence != 8 ||
			week.Over != 10 || week.Free != 0 || week.WeekendStored != 2 {
			t.Fatalf("wrong boundary week summary: %+v", week)
		}
		totals := map[string]float64{}
		var sum float64
		for _, p := range week.Projects {
			if p.Vacation || p.Color == "" {
				t.Fatal("project summary includes vacation or loses color")
			}
			totals[p.ProjectID] = p.Hours
			sum += p.Hours
		}
		if !reflect.DeepEqual(totals, map[string]float64{"p": 16, "q": 10}) || sum != week.Stored {
			t.Fatalf("project totals disagree with complete week: %v", totals)
		}
		overloaded := plan.Weeks[1]
		if len(overloaded.Projects) != 1 || overloaded.Projects[0].Hours != 60 ||
			(estimate && overloaded.Unallocated != 20) {
			t.Fatalf("unallocated hours missing from project total: %+v", overloaded)
		}
		if len(plan.Weeks[2].Projects) != 0 || plan.Weeks[2].Free != 40 || plan.Weeks[2].Over != 0 {
			t.Fatal("empty week has unexpected totals")
		}
	}
}

func monthTestWeek(t *testing.T, plan MonthPlan, monday string) MonthWeek {
	t.Helper()
	for _, w := range plan.Weeks {
		if w.Days[0].Date == monday {
			return w
		}
	}
	t.Fatalf("week %s missing", monday)
	return MonthWeek{}
}

func TestMonthAbsenceReducesCapacityWithoutDoubleCounting(t *testing.T) {
	d := monthTestData()
	d.Entries = []models.Entry{
		{Date: "2026-04-27", ProjectID: "v", Hours: 4},
		{Date: "2026-04-28", ProjectID: "v", Hours: 8},
		{Date: "2026-04-29", ProjectID: "p", Hours: 10},
		{Date: "2026-05-01", ProjectID: "v", Hours: 8}, // Public holiday: no second deduction.
		{Date: "2026-05-02", ProjectID: "v", Hours: 8}, // Weekend has no regular capacity.
	}
	before := append([]models.Entry(nil), d.Entries...)
	cal := holidays.Get(2026, "SN")
	for _, estimate := range []bool{false, true} {
		plan := buildMonthPlan(d, cal, monthTestDate("2026-05-01"), monthTestDate("2026-04-20"), estimate)
		w := monthTestWeek(t, plan, "2026-04-27")
		if w.Capacity != 20 || w.Absence != 20 || w.Stored != 10 || w.Work != 10 ||
			w.Free != 10 || w.Over != 0 || w.WeekendStored != 0 || w.Vacation != 28 {
			t.Fatalf("absence counted as work or deducted twice: %+v", w)
		}
		for i, capacity := range []float64{4, 0, 8, 8, 0, 0, 0} {
			day := w.Days[i]
			if day.Capacity != capacity {
				t.Fatalf("%s: capacity=%g, want %g", day.Date, day.Capacity, capacity)
			}
			if capacity == 0 && (day.Total != 0 || day.Stored != 0 || day.Over != 0) {
				t.Fatalf("absence-only day counted as work: %+v", day)
			}
			if day.Vacation > 0 && (len(day.Events) == 0 || !day.Events[0].Vacation) {
				t.Fatal("absence entries must remain visible")
			}
		}
		if !estimate && (w.Days[2].Total != 10 || w.Days[2].Over != 2) {
			t.Fatal("real project overtime must remain visible")
		}
	}
	if !reflect.DeepEqual(before, d.Entries) {
		t.Fatal("monthly calculations mutated entries")
	}
	// Weekly views retain their existing vacation-as-booking semantics.
	week := BuildWeek(d, cal, FYWeekIndexOf(2026, 1, monthTestDate("2026-04-27")))
	if week.ProjectTotals["v"] != 20 || week.Total != 30 {
		t.Fatalf("monthly change leaked into weekly calculation: %+v", week)
	}
}

func TestMonthHolidayVacationAndOverflow(t *testing.T) {
	for _, hours := range []float64{20, 40} {
		d := monthTestData()
		d.Entries = []models.Entry{
			{Date: "2026-05-01", ProjectID: "p", Hours: hours},
			{Date: "2026-04-28", ProjectID: "v", Hours: 8},
			{Date: "2026-04-29", ProjectID: "v", Hours: 4},
		}

		before := append([]models.Entry(nil), d.Entries...)
		plan := BuildMonthPlan(d, holidays.Get(2026, "SN"), monthTestDate("2026-05-01"), monthTestDate("2026-04-20"))
		w := monthTestWeek(t, plan, "2026-04-27")
		if w.Capacity != 20 || w.Vacation != 12 || w.Absence != 20 || w.Stored != hours || w.Unallocated != math.Max(0, hours-20) {
			t.Fatalf("wrong weekly figures: %+v", w)
		}
		var displayed float64
		for _, day := range w.Days {
			displayed += day.Total
			if day.Total > day.Capacity+1e-8 {
				t.Errorf("day exceeds capacity: %+v", day)
			}
			for _, e := range day.Events {
				if e.Estimated && (day.Weekend || day.Holiday != "" || day.Date == "2026-04-28") {
					t.Errorf("estimated on unavailable date: %+v", day)
				}
			}
		}
		if math.Abs(displayed+w.Unallocated-w.Stored) > 1e-8 {
			t.Fatal("weekly total was not preserved")
		}
		if !reflect.DeepEqual(before, d.Entries) {
			t.Fatal("preview modified stored entries")
		}
		// April and May must show the identical distribution of their shared week.
		april := BuildMonthPlan(d, holidays.Get(2026, "SN"), monthTestDate("2026-04-01"), monthTestDate("2026-04-20"))
		aw := monthTestWeek(t, april, "2026-04-27")
		for i := range w.Days {
			if !reflect.DeepEqual(w.Days[i].Events, aw.Days[i].Events) {
				t.Fatal("month boundary changed weekly distribution")
			}
		}
	}
}

func TestMonthAbsenceBoundsAtFiscalStart(t *testing.T) {
	d := monthTestData()
	d.Settings.Year, d.Settings.FiscalYearStartMonth = 2027, 7
	for i := range d.Projects {
		d.Projects[i].FiscalYear = 2027
	}
	d.Entries = []models.Entry{
		{Date: "2026-06-30", ProjectID: "v", Hours: 8},
		{Date: "2026-07-01", ProjectID: "v", Hours: 12},
		{Date: "2026-07-04", ProjectID: "v", Hours: 8},
	}
	plan := BuildStoredMonthPlan(d, holidays.Get(2026, "SN"), monthTestDate("2026-07-01"), monthTestDate("2026-07-01"))
	w := monthTestWeek(t, plan, "2026-06-29")
	if w.Capacity != 16 || w.Absence != 8 || w.Vacation != 20 || w.Stored != 0 ||
		w.WeekendStored != 0 || w.Over != 0 || w.Days[2].Over != 0 || w.Days[2].Capacity != 0 {
		t.Fatalf("absence must be capped to in-year weekday capacity: %+v", w)
	}
}

func TestMonthHistoryAcrossAssignmentsAndPastFixed(t *testing.T) {
	d := monthTestData()
	d.Projects = append(d.Projects, models.Project{ID: "old", AssignmentID: "123", FiscalYear: 2025})
	d.Entries = []models.Entry{
		{Date: "2025-12-01", ProjectID: "old", Hours: 8},
		{Date: "2025-12-08", ProjectID: "old", Hours: 8},
		{Date: "2025-12-15", ProjectID: "old", Hours: 8},
		{Date: "2025-12-22", ProjectID: "old", Hours: 8},
		{Date: "2026-01-16", ProjectID: "p", Hours: 8},
		{Date: "2026-01-05", ProjectID: "p", Hours: 3},
		{Date: "2026-01-09", ProjectID: "p", Hours: 8},
		{Date: "2026-01-08", ProjectID: "missing", Hours: 200},
	}
	plan := BuildMonthPlan(d, holidays.Get(2026, "SN"), monthTestDate("2026-01-01"), monthTestDate("2026-01-07"))
	future := monthTestWeek(t, plan, "2026-01-12")
	if future.Days[0].Total != 8 || future.Days[4].Total != 0 || !strings.Contains(future.Days[0].Events[0].Basis, "4 Wochen") {
		t.Fatalf("historical Monday preference lost: %+v", future)
	}
	current := monthTestWeek(t, plan, "2026-01-05")
	if current.Days[0].Total != 3 || current.Days[0].Events[0].Estimated || current.Days[1].Total != 0 || current.Stored != 11 {
		t.Fatalf("past entries changed or orphan counted: %+v", current)
	}
	var futureHours float64
	for _, day := range current.Days[2:] {
		futureHours += day.Total
	}
	if math.Abs(futureHours-8) > 1e-8 {
		t.Fatal("current-week forecast must stay on remaining weekdays")
	}
}

func TestMonthStoredAndFiscalBoundaries(t *testing.T) {
	d := monthTestData()
	d.Settings.Year = 2027
	d.Settings.FiscalYearStartMonth = 7
	d.Projects[0].Active = false // Existing hours must not vanish for finished projects.
	d.Entries = []models.Entry{
		{Date: "2026-06-29", ProjectID: "p", Hours: 30},
		{Date: "2026-07-04", ProjectID: "p", Hours: 12},
	}
	cal := holidays.Get(2027, "SN")
	plan := BuildMonthPlan(d, cal, monthTestDate("2020-01-01"), monthTestDate("2026-07-06"))
	if plan.Month != "2026-07" || plan.Prev != "" || len(plan.Weeks) != 5 {
		t.Fatalf("incorrect month bounds: %+v", plan)
	}
	w := plan.Weeks[0]
	if w.Stored != 12 || w.WeekendStored != 12 || w.Capacity != 24 || w.Days[0].Total != 0 || w.Days[5].Total != 12 || w.Days[5].Over != 12 {
		t.Fatalf("stored view / FY boundary wrong: %+v", w)
	}
	estimated := BuildMonthPlan(d, cal, monthTestDate("2026-07-01"), monthTestDate("2026-06-01")).Weeks[0]
	if estimated.Days[2].Total != 4 || estimated.Days[3].Total != 4 || estimated.Days[4].Total != 4 || estimated.Days[5].Total != 0 {
		t.Fatalf("weekend forecast not redistributed within FY: %+v", estimated)
	}
	last := BuildMonthPlan(d, cal, monthTestDate("2030-01-01"), monthTestDate("2026-06-01"))
	if last.Month != "2027-06" || last.Next != "" {
		t.Fatal("last FY month not clamped")
	}
	leap := monthTestData()
	leap.Settings.Year = 2024
	lp := BuildMonthPlan(leap, holidays.Get(2024, "SN"), monthTestDate("2024-02-01"), monthTestDate("2024-01-01"))
	count := 0
	for _, w := range lp.Weeks {
		for _, day := range w.Days {
			if day.InMonth {
				count++
			}
		}
	}
	if count != 29 {
		t.Fatalf("leap month has %d days", count)
	}
}

func TestMonthWindowsCompetitionAndNoCapacity(t *testing.T) {
	d := monthTestData()
	d.Projects = append(d.Projects, models.Project{ID: "narrow", Name: "Narrow", StartDate: "2026-03-02", EndDate: "2026-03-02"})
	d.Entries = []models.Entry{
		{Date: "2026-03-06", ProjectID: "p", Hours: 32},
		{Date: "2026-03-06", ProjectID: "narrow", Hours: 8},
	}

	cal := holidays.Get(2026, "SN")
	w := monthTestWeek(t, BuildMonthPlan(d, cal, monthTestDate("2026-03-01"), monthTestDate("2026-02-01")), "2026-03-02")
	if w.Unallocated != 0 || w.Days[0].Events[0].ProjectID != "narrow" {
		t.Fatalf("narrow window not prioritized: %+v", w)
	}
	for _, day := range w.Days[:5] {
		if math.Abs(day.Total-8) > 1e-8 {
			t.Fatalf("shared daily capacity wrong: %+v", day)
		}
	}
	for i := 0; i < 5; i++ {
		d.Entries = append(d.Entries, models.Entry{Date: monthTestDate("2026-03-02").AddDate(0, 0, i).Format("2006-01-02"), ProjectID: "v", Hours: 8})
	}
	w = monthTestWeek(t, BuildMonthPlan(d, cal, monthTestDate("2026-03-01"), monthTestDate("2026-02-01")), "2026-03-02")
	if w.Unallocated != 40 || len(w.Pending) != 2 {
		t.Fatalf("full vacation must retain unallocated forecast: %+v", w)
	}
}

func TestMonthFractionalConservationAndStableEntryOrder(t *testing.T) {
	d := monthTestData()
	d.Projects = append(d.Projects, models.Project{ID: "second", Name: "Second"})
	cal := holidays.Get(2026, "SN")
	for n := 1; n < 80; n++ {
		d.Entries = []models.Entry{
			{Date: "2026-03-06", ProjectID: "p", Hours: float64(n) / 3},
			{Date: "2026-03-05", ProjectID: "second", Hours: float64(n) / 7},
			{Date: "2026-03-02", ProjectID: "v", Hours: 3.5},
		}
		build := func() MonthPlan {
			return BuildMonthPlan(d, cal, monthTestDate("2026-03-01"), monthTestDate("2026-02-01"))
		}
		plan := build()
		w := monthTestWeek(t, plan, "2026-03-02")
		byProject := make(map[string]float64)
		for _, day := range w.Days {
			if day.Total > day.Capacity+1e-8 {
				t.Fatalf("overfilled day: %+v", day)
			}
			for _, e := range day.Events {
				byProject[e.ProjectID] += e.Hours
			}
		}
		for _, e := range w.Pending {
			byProject[e.ProjectID] += e.Hours
		}
		for _, e := range d.Entries {
			if math.Abs(byProject[e.ProjectID]-e.Hours) > 1e-8 {
				t.Fatalf("lost fractional hours for %s: %g != %g", e.ProjectID, byProject[e.ProjectID], e.Hours)
			}
		}
		d.Entries[0], d.Entries[1] = d.Entries[1], d.Entries[0]
		if !reflect.DeepEqual(plan, build()) {
			t.Fatal("entry order changed the estimate")
		}
	}
}
