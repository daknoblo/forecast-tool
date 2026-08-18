package forecast

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// This file holds the cross-cutting invariants of the calculation chain. Where
// the unit tests in agg_test.go pin down single expected numbers, these walk
// generated documents and assert the relationships that must hold for ANY
// input: every roll-up has to equal the sum of its parts, forecast plus booked
// has to equal the projection, and nothing may be counted twice.

// eq compares two hour figures with a tolerance that absorbs the 0.1 rounding
// applied at every roll-up level.
func eq(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// pastFY / futureFY are fiscal years that are certainly over resp. certainly
// ahead, so "today" never falls inside them. That makes booked-vs-forecast
// deterministic without freezing the clock.
func pastFY() int   { return time.Now().UTC().Year() - 3 }
func futureFY() int { return time.Now().UTC().Year() + 3 }

// currentFY is the fiscal year containing today for the given start month.
func currentFY(startMonth int) int {
	return FiscalYearOf(time.Now().UTC().Truncate(24*time.Hour), startMonth)
}

// genData builds a document for the given fiscal year with several projects and
// a deterministic, pseudo-random spread of hours over every weekday of the year.
// The hour values deliberately carry decimals so rounding differences between
// the aggregation levels surface.
func genData(year, startMonth int, seed int64) models.Data {
	d := models.Data{
		Settings: models.Settings{
			Year:                 year,
			FederalState:         "BY",
			WeeklyTargetHours:    40,
			FiscalYearStartMonth: startMonth,
			Utilization:          models.DefaultUtilization(),
		},
		FiscalYears: map[int]models.FiscalYearSettings{
			year: {VacationDays: 30, StandardTaskHours: 120, StandardTaskLabel: "Regelaufgaben"},
		},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "5000001", Name: "Alpha", BudgetHours: 600, Color: "#2563eb", Active: true, FiscalYear: year},
			{ID: "p2", AssignmentID: "5000002", Name: "Beta", BudgetHours: 400, Color: "#0d9488", Active: true, FiscalYear: year},
			{ID: "p3", AssignmentID: "5000003", Name: "Gamma", BudgetHours: 250, Color: "#d97706", Active: true, FiscalYear: year},
			{
				ID: models.VacationProjectID(year), Name: "Urlaub", BudgetHours: 240,
				Color: models.VacationColor, Active: true, FiscalYear: year, System: models.VacationSystem,
			},
		},
	}

	start, end := FiscalYear(year, startMonth)
	// Simple deterministic LCG: no dependency on math/rand's global state.
	next := func() int64 {
		seed = (seed*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		return seed >> 16
	}
	pids := []string{"p1", "p2", "p3", models.VacationProjectID(year)}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		iso := day.Format("2006-01-02")
		for _, pid := range pids {
			if next()%3 == 0 {
				continue // not every project is booked on every day
			}
			// 0.1 .. 4.0 hours, one decimal
			h := float64(next()%40+1) / 10
			d.Entries = append(d.Entries, models.Entry{Date: iso, ProjectID: pid, Hours: h})
		}
	}
	return d
}

// calFor returns a holiday calendar covering the given fiscal year.
func calFor(d models.Data) *holidays.Calendar {
	return holidays.New(d.Settings.Year, d.Settings.FederalState)
}

// entryHours sums the hours of the entries matching the filter.
func entryHours(d models.Data, keep func(models.Entry) bool) float64 {
	var sum float64
	for _, e := range d.Entries {
		if keep(e) {
			sum += e.Hours
		}
	}
	return sum
}

// --- calendar / fiscal-year arithmetic -------------------------------------

// Every day of FY(y) must map back to y, the fiscal years must tile the
// calendar without gaps or overlaps, and the end must be the day before the
// next year's start.
func TestFiscalYearTilesTheCalendar(t *testing.T) {
	for startMonth := 1; startMonth <= 12; startMonth++ {
		for year := 2024; year <= 2028; year++ {
			start, end := FiscalYear(year, startMonth)
			if int(start.Month()) != startMonth || start.Day() != 1 {
				t.Fatalf("sm=%d fy=%d: start = %s, want the 1st of month %d", startMonth, year, start.Format("2006-01-02"), startMonth)
			}
			nextStart, _ := FiscalYear(year+1, startMonth)
			if got := end.AddDate(0, 0, 1); !got.Equal(nextStart) {
				t.Fatalf("sm=%d fy=%d: end+1 = %s, want the next FY start %s",
					startMonth, year, got.Format("2006-01-02"), nextStart.Format("2006-01-02"))
			}
			for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
				if got := FiscalYearOf(day, startMonth); got != year {
					t.Fatalf("sm=%d: FiscalYearOf(%s) = %d, want %d", startMonth, day.Format("2006-01-02"), got, year)
				}
			}
		}
	}
}

// Week indexing must cover the whole fiscal year: every day lands in a week
// between 1 and FYWeeks, and that week's Monday really contains the day.
func TestFYWeekIndexCoversTheYear(t *testing.T) {
	for startMonth := 1; startMonth <= 12; startMonth++ {
		for year := 2025; year <= 2028; year++ {
			start, end := FiscalYear(year, startMonth)
			weeks := FYWeeks(year, startMonth)
			seen := map[int]bool{}
			for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
				w := FYWeekIndexOf(year, startMonth, day)
				if w < 1 || w > weeks {
					t.Fatalf("sm=%d fy=%d: %s -> week %d, want 1..%d", startMonth, year, day.Format("2006-01-02"), w, weeks)
				}
				seen[w] = true
				monday := FYWeekMonday(year, startMonth, w)
				if day.Before(monday) || day.After(monday.AddDate(0, 0, 6)) {
					t.Fatalf("sm=%d fy=%d: %s is not inside week %d (Monday %s)",
						startMonth, year, day.Format("2006-01-02"), w, monday.Format("2006-01-02"))
				}
			}
			for w := 1; w <= weeks; w++ {
				if !seen[w] {
					t.Errorf("sm=%d fy=%d: week %d has no day in the fiscal year", startMonth, year, w)
				}
			}
			// Dates outside the fiscal year must be rejected, not clamped.
			if got := FYWeekIndexOf(year, startMonth, start.AddDate(0, 0, -1)); got != 0 {
				t.Errorf("sm=%d fy=%d: day before the FY -> %d, want 0", startMonth, year, got)
			}
			if got := FYWeekIndexOf(year, startMonth, end.AddDate(0, 0, 1)); got != 0 {
				t.Errorf("sm=%d fy=%d: day after the FY -> %d, want 0", startMonth, year, got)
			}
		}
	}
}

// CurrentFYWeek is clamped into the fiscal year for past, current and future
// years alike.
func TestCurrentFYWeekIsClamped(t *testing.T) {
	for _, year := range []int{pastFY(), currentFY(7), futureFY()} {
		w := CurrentFYWeek(year, 7)
		if max := FYWeeks(year, 7); w < 1 || w > max {
			t.Errorf("fy=%d: CurrentFYWeek = %d, want 1..%d", year, w, max)
		}
	}
}

// round1 must round half away from zero and stay symmetric around zero.
func TestRound1(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0},
		{0.04, 0},
		{0.05, 0.1},
		{-0.05, -0.1},
		{-0.04, 0},
		{2.44, 2.4},
		{2.45, 2.5},
		{-2.45, -2.5},
		{1234.56, 1234.6},
		{-1234.56, -1234.6},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, v := range []float64{0.35, -0.35, 12.75, -12.75} {
		if round1(v) != -round1(-v) {
			t.Errorf("round1 is not symmetric at %v: %v vs %v", v, round1(v), -round1(-v))
		}
	}
}

// --- goal summary ----------------------------------------------------------

// Every roll-up of the goal page has to carry the same total: months, quarters
// and halves all partition the fiscal year, and booked plus forecast is the
// projection.
func TestGoalSummaryRollUpsAreConsistent(t *testing.T) {
	for _, year := range []int{pastFY(), currentFY(7), futureFY()} {
		for _, startMonth := range []int{1, 4, 7, 10} {
			fy := year
			if startMonth != 7 {
				fy = FiscalYearOf(time.Now().UTC(), startMonth) + (year - currentFY(7))
			}
			d := genData(fy, startMonth, int64(fy*100+startMonth))
			gs := BuildGoalSummary(d, calFor(d))
			name := fmt.Sprintf("fy=%d sm=%d", fy, startMonth)

			vac := vacationSet(d.Projects)
			start, end := FiscalYear(fy, startMonth)
			startISO, endISO := start.Format("2006-01-02"), end.Format("2006-01-02")
			want := entryHours(d, func(e models.Entry) bool {
				return !vac[e.ProjectID] && e.Date >= startISO && e.Date <= endISO
			})
			if !eq(gs.Projected, want, 0.1) {
				t.Errorf("%s: Projected = %v, want %v", name, gs.Projected, want)
			}
			if !eq(gs.ActualTotal+gs.ForecastRemaining, gs.Projected, 0.2) {
				t.Errorf("%s: booked %v + forecast %v != projected %v",
					name, gs.ActualTotal, gs.ForecastRemaining, gs.Projected)
			}

			sum := func(ps []PeriodStat, f func(PeriodStat) float64) float64 {
				var s float64
				for _, p := range ps {
					s += f(p)
				}
				return s
			}
			proj := func(p PeriodStat) float64 { return p.Projected }
			if got := sum(gs.Months, proj); !eq(got, gs.Projected, 1.2) {
				t.Errorf("%s: months sum to %v, want %v", name, got, gs.Projected)
			}
			if got := sum(gs.Quarters, proj); !eq(got, gs.Projected, 0.4) {
				t.Errorf("%s: quarters sum to %v, want %v", name, got, gs.Projected)
			}
			if got := sum(gs.Halves, proj); !eq(got, gs.Projected, 0.2) {
				t.Errorf("%s: halves sum to %v, want %v", name, got, gs.Projected)
			}
			for i, m := range gs.Months {
				if !eq(m.Actual+m.Forecast, m.Projected, 0.2) {
					t.Errorf("%s: month %d: %v booked + %v forecast != %v projected", name, i, m.Actual, m.Forecast, m.Projected)
				}
			}
			for i, q := range gs.Quarters {
				if !eq(q.Actual+q.Forecast, q.Projected, 0.2) {
					t.Errorf("%s: quarter %d: %v booked + %v forecast != %v projected", name, i, q.Actual, q.Forecast, q.Projected)
				}
			}
			// The evenly split period goals have to add back up to the FY goal.
			if got := sum(gs.Quarters, func(p PeriodStat) float64 { return p.Target }); !eq(got, gs.TargetHours, 0.4) {
				t.Errorf("%s: quarter targets sum to %v, want %v", name, got, gs.TargetHours)
			}
			if got := sum(gs.Halves, func(p PeriodStat) float64 { return p.Target }); !eq(got, gs.TargetHours, 0.2) {
				t.Errorf("%s: half-year targets sum to %v, want %v", name, got, gs.TargetHours)
			}
			if !eq(gs.Remaining, gs.TargetHours-gs.Projected, 0.2) {
				t.Errorf("%s: Remaining = %v, want %v", name, gs.Remaining, gs.TargetHours-gs.Projected)
			}
			if gs.TargetHours > 0 {
				if !eq(gs.PctProjected, gs.Projected/gs.TargetHours*100, 0.2) {
					t.Errorf("%s: PctProjected = %v, want %v", name, gs.PctProjected, gs.Projected/gs.TargetHours*100)
				}
			}
			if gs.WorkingDaysDone > gs.WorkingDaysYear {
				t.Errorf("%s: %d working days done > %d in the year", name, gs.WorkingDaysDone, gs.WorkingDaysYear)
			}
		}
	}
}

// A fiscal year that is completely over has no forecast left, and one that has
// not started yet has nothing booked.
func TestGoalSummaryBookedForecastSplitByFiscalYear(t *testing.T) {
	past := genData(pastFY(), 7, 11)
	gs := BuildGoalSummary(past, calFor(past))
	if gs.ForecastRemaining != 0 {
		t.Errorf("finished FY: forecast = %v, want 0", gs.ForecastRemaining)
	}
	if !eq(gs.ActualTotal, gs.Projected, 0.1) {
		t.Errorf("finished FY: booked %v != projected %v", gs.ActualTotal, gs.Projected)
	}
	if gs.WorkingDaysDone != gs.WorkingDaysYear {
		t.Errorf("finished FY: %d of %d working days done, want all", gs.WorkingDaysDone, gs.WorkingDaysYear)
	}

	future := genData(futureFY(), 7, 12)
	gs = BuildGoalSummary(future, calFor(future))
	if gs.ActualTotal != 0 {
		t.Errorf("future FY: booked = %v, want 0", gs.ActualTotal)
	}
	if !eq(gs.ForecastRemaining, gs.Projected, 0.1) {
		t.Errorf("future FY: forecast %v != projected %v", gs.ForecastRemaining, gs.Projected)
	}
	if gs.WorkingDaysDone != 0 {
		t.Errorf("future FY: %d working days done, want 0", gs.WorkingDaysDone)
	}
}

// --- year summary ----------------------------------------------------------

// Per project and in total, the budget arithmetic has to add up exactly.
func TestYearSummaryBudgetArithmetic(t *testing.T) {
	for _, year := range []int{pastFY(), currentFY(7), futureFY()} {
		d := genData(year, 7, int64(year))
		ys := BuildYearSummary(d, calFor(d))
		name := fmt.Sprintf("fy=%d", year)

		var totHours, totBudget, totCarry, totReleased float64
		for _, ps := range ys.Projects {
			id := ps.Project.ID
			if !eq(ps.Actual+ps.Forecast, ps.Consumed, 0.2) {
				t.Errorf("%s %s: %v booked + %v forecast != %v consumed", name, id, ps.Actual, ps.Forecast, ps.Consumed)
			}
			if !eq(ps.AvailableBudget, ps.Project.BudgetHours-ps.CarryOver-ps.Released, 0.2) {
				t.Errorf("%s %s: available %v != budget %v - carry %v - released %v",
					name, id, ps.AvailableBudget, ps.Project.BudgetHours, ps.CarryOver, ps.Released)
			}
			if !eq(ps.Remaining, ps.AvailableBudget-ps.Consumed, 0.2) {
				t.Errorf("%s %s: remaining %v != available %v - consumed %v", name, id, ps.Remaining, ps.AvailableBudget, ps.Consumed)
			}
			if ps.RemainingWorkdays > ps.WindowWorkdays {
				t.Errorf("%s %s: %d remaining workdays > %d in the window", name, id, ps.RemainingWorkdays, ps.WindowWorkdays)
			}
			if ps.StartDate > ps.EndDate {
				t.Errorf("%s %s: window %s..%s is inverted", name, id, ps.StartDate, ps.EndDate)
			}
			if !ps.Project.IsVacation() {
				totHours += ps.Consumed
				totBudget += ps.Project.BudgetHours
				totCarry += ps.CarryOver
				totReleased += ps.Released
			}
		}
		if !eq(ys.TotalHours, totHours, 0.4) {
			t.Errorf("%s: TotalHours = %v, want %v", name, ys.TotalHours, totHours)
		}
		if !eq(ys.TotalBudget, totBudget, 0.2) {
			t.Errorf("%s: TotalBudget = %v, want %v", name, ys.TotalBudget, totBudget)
		}
		if !eq(ys.TotalAvailable, ys.TotalBudget-ys.TotalCarryOver-ys.TotalReleased, 0.2) {
			t.Errorf("%s: TotalAvailable = %v, want %v", name, ys.TotalAvailable, ys.TotalBudget-ys.TotalCarryOver-ys.TotalReleased)
		}
		if !eq(ys.TotalRemaining, ys.TotalAvailable-ys.TotalHours, 0.2) {
			t.Errorf("%s: TotalRemaining = %v, want %v", name, ys.TotalRemaining, ys.TotalAvailable-ys.TotalHours)
		}
		if !eq(ys.TotalActual+ys.TotalForecast, ys.TotalHours, 0.4) {
			t.Errorf("%s: %v booked + %v forecast != %v total", name, ys.TotalActual, ys.TotalForecast, ys.TotalHours)
		}
	}
}

// The weekly totals partition the fiscal year: they must add up to every hour
// dated inside it - vacation included, because the weekly utilization counts
// the time that is actually consumed.
func TestWeekTotalsPartitionTheYear(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 42)
	ys := BuildYearSummary(d, calFor(d))

	start, end := FiscalYear(year, 7)
	startISO, endISO := start.Format("2006-01-02"), end.Format("2006-01-02")
	want := entryHours(d, func(e models.Entry) bool { return e.Date >= startISO && e.Date <= endISO })

	var sum, actual, forecast float64
	last := 0
	for _, wt := range ys.WeekTotals {
		if !eq(wt.Actual+wt.Forecast, wt.Hours, 0.2) {
			t.Errorf("week %d: %v booked + %v forecast != %v hours", wt.Week, wt.Actual, wt.Forecast, wt.Hours)
		}
		if wt.TargetHours > 0 && !eq(wt.UtilizationPct, wt.Hours/wt.TargetHours*100, 0.2) {
			t.Errorf("week %d: utilization %v != %v", wt.Week, wt.UtilizationPct, wt.Hours/wt.TargetHours*100)
		}
		if wt.Status.Hours != wt.Hours {
			t.Errorf("week %d: status classified %v hours, but the row shows %v", wt.Week, wt.Status.Hours, wt.Hours)
		}
		sum += wt.Hours
		actual += wt.Actual
		forecast += wt.Forecast
		if wt.Hours > 0 {
			last = wt.Week
		}
	}
	if !eq(sum, want, float64(len(ys.WeekTotals))*0.05+0.1) {
		t.Errorf("week totals sum to %v, want %v", sum, want)
	}
	if !eq(actual+forecast, sum, 1) {
		t.Errorf("weekly booked %v + forecast %v != %v", actual, forecast, sum)
	}
	if ys.LastPlannedWeek != last {
		t.Errorf("LastPlannedWeek = %d, want %d", ys.LastPlannedWeek, last)
	}
}

// A span aggregates exactly the weeks it spans - no day may be dropped or
// counted twice at the block boundaries.
func TestSpanEqualsSumOfItsWeeks(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 7)
	cal := calFor(d)

	for _, weeks := range []int{1, 2, 4, 6} {
		sv := BuildSpan(d, cal, 10, weeks)
		if len(sv.Blocks) != weeks {
			t.Fatalf("weeks=%d: %d blocks", weeks, len(sv.Blocks))
		}
		if len(sv.Days) != weeks*5 {
			t.Fatalf("weeks=%d: %d days, want %d", weeks, len(sv.Days), weeks*5)
		}
		var want, holiday float64
		perProject := map[string]float64{}
		for i := 0; i < weeks; i++ {
			wv := BuildWeek(d, cal, sv.StartWeek+i)
			want += wv.Total
			holiday += wv.HolidayHours
			for pid, h := range wv.ProjectTotals {
				perProject[pid] += h
			}
		}
		if !eq(sv.Total, want, 0.2) {
			t.Errorf("weeks=%d: span total %v != %v", weeks, sv.Total, want)
		}
		if !eq(sv.HolidayHours, holiday, 0.2) {
			t.Errorf("weeks=%d: span holidays %v != %v", weeks, sv.HolidayHours, holiday)
		}
		for pid, h := range perProject {
			if !eq(sv.ProjectTotals[pid], h, 0.2) {
				t.Errorf("weeks=%d %s: span %v != %v", weeks, pid, sv.ProjectTotals[pid], h)
			}
		}
		if !eq(sv.TargetHours, d.Settings.WeeklyTargetHours*float64(weeks), 0.1) {
			t.Errorf("weeks=%d: target %v", weeks, sv.TargetHours)
		}
	}
}

// A single week must equal the sum of its five day cells, per project and in
// total.
func TestWeekEqualsSumOfItsDays(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 99)
	cal := calFor(d)
	for w := 1; w <= 8; w++ {
		wv := BuildWeek(d, cal, w)
		var total float64
		perProject := map[string]float64{}
		for _, c := range wv.Days {
			var cellSum float64
			for pid, h := range c.Hours {
				cellSum += h
				perProject[pid] += h
			}
			if !eq(cellSum, c.Total, 0.001) {
				t.Errorf("week %d %s: cell total %v != %v", w, c.Date, c.Total, cellSum)
			}
			total += c.Total
		}
		if !eq(wv.Total, total, 0.1) {
			t.Errorf("week %d: total %v != %v", w, wv.Total, total)
		}
		for pid, h := range perProject {
			if !eq(wv.ProjectTotals[pid], h, 0.001) {
				t.Errorf("week %d %s: %v != %v", w, pid, wv.ProjectTotals[pid], h)
			}
		}
	}
}

// --- capacity --------------------------------------------------------------

// The capacity breakdown is a plain subtraction chain and must never go
// negative, whatever the overrides say.
func TestFYCapacityNeverGoesNegative(t *testing.T) {
	year := currentFY(7)
	base := genData(year, 7, 3)
	cal := calFor(base)

	for _, fy := range []models.FiscalYearSettings{
		{VacationDays: 30, StandardTaskHours: 120},
		{VacationDays: 366, StandardTaskHours: 100000},
		{WeekdayHours: 10, VacationDays: 30},
		{VacationDays: 0, HolidayDays: intPtr(0)},
		{WeekdayHours: 1600, VacationDays: 25, HolidayDays: intPtr(11), StandardTaskHours: 80},
	} {
		d := base
		d.FiscalYears = map[int]models.FiscalYearSettings{year: fy}
		c := BuildFYCapacity(d, cal, year)
		if c.RemainingHours < 0 {
			t.Errorf("%+v: RemainingHours = %v, want >= 0", fy, c.RemainingHours)
		}
		want := c.WeekdayHours - c.VacationHours - c.HolidayHours - c.StandardTaskHours
		if want < 0 {
			want = 0
		}
		if !eq(c.RemainingHours, want, 0.2) {
			t.Errorf("%+v: RemainingHours = %v, want %v", fy, c.RemainingHours, want)
		}
		if !eq(c.VacationHours, float64(c.VacationDays)*HolidayDayHours, 0.001) {
			t.Errorf("%+v: vacation hours %v", fy, c.VacationHours)
		}
		if !eq(c.HolidayHours, float64(c.HolidayDays)*HolidayDayHours, 0.001) {
			t.Errorf("%+v: holiday hours %v", fy, c.HolidayHours)
		}
		if c.HoursOverridden != (fy.WeekdayHours > 0) {
			t.Errorf("%+v: HoursOverridden = %v", fy, c.HoursOverridden)
		}
		if c.HolidayOverridden != (fy.HolidayDays != nil) {
			t.Errorf("%+v: HolidayOverridden = %v", fy, c.HolidayOverridden)
		}
	}
}

// The goal target is exactly the capacity left over, and the goal page must
// report the same breakdown as the settings page.
func TestGoalTargetEqualsCapacity(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 5)
	cal := calFor(d)
	c := BuildFYCapacity(d, cal, year)
	gs := BuildGoalSummary(d, cal)

	if !eq(gs.TargetHours, c.RemainingHours, 0.001) {
		t.Errorf("target %v != capacity %v", gs.TargetHours, c.RemainingHours)
	}
	if gs.AvailableHours != c.RemainingHours || gs.WeekdayHours != c.WeekdayHours ||
		gs.VacationHours != c.VacationHours || gs.HolidayHours != c.HolidayHours {
		t.Errorf("goal capacity block does not match BuildFYCapacity:\n goal %+v\n cap  %+v", gs, c)
	}
	if !eq(gs.TargetPerQuarter*4, gs.TargetHours, 0.4) {
		t.Errorf("4 * %v != %v", gs.TargetPerQuarter, gs.TargetHours)
	}
	if !eq(gs.TargetPerMonth*12, gs.TargetHours, 1.2) {
		t.Errorf("12 * %v != %v", gs.TargetPerMonth, gs.TargetHours)
	}
}

// --- sankey ----------------------------------------------------------------

// Every bucket has to equal the sum of its project bands, and the span totals
// have to equal the sum of the buckets - otherwise the stacked bands do not
// fill the column they are drawn in.
func TestSankeyBucketsAreConsistent(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 21)
	cal := calFor(d)

	for _, r := range SankeyRanges {
		data := BuildSankey(d, cal, r.Key, 0)
		var total, capacity float64
		perProject := map[string]float64{}
		for _, b := range data.Buckets {
			var bandSum float64
			for pid, h := range b.Hours {
				bandSum += h
				perProject[pid] += h
			}
			if !eq(bandSum, b.Total, 0.1) {
				t.Errorf("%s %s: bands sum to %v, but the bucket says %v", r.Key, b.Label, bandSum, b.Total)
			}
			if !eq(b.CapacityHours, b.WeekdayHours-b.HolidayHours, 0.001) {
				t.Errorf("%s %s: capacity %v != %v - %v", r.Key, b.Label, b.CapacityHours, b.WeekdayHours, b.HolidayHours)
			}
			if !eq(b.FreeHours, b.CapacityHours-b.Total, 0.001) {
				t.Errorf("%s %s: free %v != %v - %v", r.Key, b.Label, b.FreeHours, b.CapacityHours, b.Total)
			}
			if b.Total > data.MaxBucket {
				t.Errorf("%s %s: bucket %v exceeds MaxBucket %v", r.Key, b.Label, b.Total, data.MaxBucket)
			}
			total += b.Total
			capacity += b.CapacityHours
		}
		if !eq(data.Total, total, 0.1) {
			t.Errorf("%s: Total = %v, want %v", r.Key, data.Total, total)
		}
		if !eq(data.CapacityTotal, capacity, 0.1) {
			t.Errorf("%s: CapacityTotal = %v, want %v", r.Key, data.CapacityTotal, capacity)
		}
		if !eq(data.FreeTotal, data.CapacityTotal-data.Total, 0.1) {
			t.Errorf("%s: FreeTotal = %v, want %v", r.Key, data.FreeTotal, data.CapacityTotal-data.Total)
		}
		var projSum float64
		for pid, h := range data.ProjectTotals {
			projSum += h
			if !eq(perProject[pid], h, 0.1) {
				t.Errorf("%s %s: project total %v != %v", r.Key, pid, h, perProject[pid])
			}
		}
		// Buckets and project totals are rounded independently, so the two sums
		// may drift by half a tenth per rounded value.
		tol := 0.05*float64(len(data.Buckets)+len(data.ProjectTotals)) + 0.1
		if !eq(projSum, data.Total, tol) {
			t.Errorf("%s: project totals sum to %v, but the span total is %v", r.Key, projSum, data.Total)
		}
		// Every project with hours must have a band, in a stable stack order.
		if len(data.Projects) != len(data.ProjectTotals) {
			t.Errorf("%s: %d bands for %d projects with hours", r.Key, len(data.Projects), len(data.ProjectTotals))
		}
	}
}

// Shifting the horizon must never leave the fiscal year, and the navigation
// flags have to match the clamping.
func TestSankeyOffsetStaysInsideTheYear(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 8)
	cal := calFor(d)
	maxW := FYWeeks(year, 7)

	for _, r := range SankeyRanges {
		for _, off := range []int{-1000, -60, -1, 0, 1, 60, 1000} {
			data := BuildSankey(d, cal, r.Key, off)
			if len(data.Buckets) == 0 {
				t.Fatalf("%s off=%d: no buckets", r.Key, off)
			}
			if len(data.Buckets) > maxW {
				t.Errorf("%s off=%d: %d buckets > %d weeks", r.Key, off, len(data.Buckets), maxW)
			}
			for _, b := range data.Buckets {
				if b.WeekdayHours > 5*HolidayDayHours {
					t.Errorf("%s off=%d %s: %v weekday hours in one week", r.Key, off, b.Label, b.WeekdayHours)
				}
			}
			if data.RangeLabel == "" {
				t.Errorf("%s off=%d: no range label", r.Key, off)
			}
		}
	}
}

// --- goal flow -------------------------------------------------------------

// Each stage of the Sankey carries the whole fiscal year, so all five columns
// and all four link layers must sum to the same total.
func TestGoalFlowStagesCarryTheSameTotal(t *testing.T) {
	for _, year := range []int{pastFY(), currentFY(7), futureFY()} {
		d := genData(year, 7, int64(year*7))
		flow := BuildGoalFlow(d, calFor(d))
		name := fmt.Sprintf("fy=%d", year)
		if !flow.HasData {
			t.Fatalf("%s: flow has no data", name)
		}
		for i, stage := range flow.Stages {
			var hours, booked float64
			for _, n := range stage {
				hours += n.Hours
				booked += n.Booked
				if n.Booked > n.Hours+0.05 {
					t.Errorf("%s stage %d %s: booked %v > hours %v", name, i, n.ID, n.Booked, n.Hours)
				}
			}
			if !eq(hours, flow.Total, 1.2) {
				t.Errorf("%s: stage %d sums to %v, want %v", name, i, hours, flow.Total)
			}
			if !eq(booked, flow.Booked, 1.2) {
				t.Errorf("%s: stage %d booked sums to %v, want %v", name, i, booked, flow.Booked)
			}
		}
		for stage := 0; stage < 4; stage++ {
			var hours float64
			for _, l := range flow.Links {
				if l.Stage == stage {
					hours += l.Hours
				}
			}
			if !eq(hours, flow.Total, 1.2) {
				t.Errorf("%s: link layer %d sums to %v, want %v", name, stage, hours, flow.Total)
			}
		}
		// Vacation never flows into the goal.
		for _, n := range flow.Stages[0] {
			if n.ID == "p:"+models.VacationProjectID(year) {
				t.Errorf("%s: vacation appears as a goal-flow project", name)
			}
		}
	}
}

// --- burndown --------------------------------------------------------------

// The burn-down starts at the full budget and ends at budget minus every hour
// booked inside the window; it never rises.
func TestBurndownIsMonotonic(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 4)
	ys := BuildYearSummary(d, calFor(d))

	for _, ps := range ys.Projects {
		pts := BuildBurndown(d, ps.Project.ID, ps.StartDate, ps.EndDate, ps.AvailableBudget)
		if len(pts) < 2 {
			t.Fatalf("%s: %d burndown points", ps.Project.ID, len(pts))
		}
		if !eq(pts[0].Remaining, ps.AvailableBudget, ps.AvailableBudget) {
			t.Errorf("%s: first point %v", ps.Project.ID, pts[0].Remaining)
		}
		for i := 1; i < len(pts); i++ {
			if pts[i].Remaining > pts[i-1].Remaining+0.001 {
				t.Errorf("%s: burndown rises at %d (%v -> %v)", ps.Project.ID, i, pts[i-1].Remaining, pts[i].Remaining)
			}
		}
		want := ps.AvailableBudget - entryHours(d, func(e models.Entry) bool {
			return e.ProjectID == ps.Project.ID && e.Date >= ps.StartDate && e.Date <= ps.EndDate
		})
		if got := pts[len(pts)-1].Remaining; !eq(got, want, 0.2) {
			t.Errorf("%s: last point %v, want %v", ps.Project.ID, got, want)
		}
	}
}

// --- utilization -----------------------------------------------------------

// The traffic light has to be continuous: every hour value gets exactly one
// tier and the tiers rise monotonically.
func TestUtilizationTiersAreContinuous(t *testing.T) {
	s := models.Settings{Utilization: models.DefaultUtilization()}
	u := s.Utilization
	cases := []struct {
		hours float64
		key   string
	}{
		{0, "min"},
		{u.MinHours - 0.1, "min"},
		{u.MinHours, "min"},
		{u.MinHours + 0.1, "optimal"},
		{u.OptimalHours, "optimal"},
		{u.OptimalHours + 0.1, "high"},
		{u.OverHours - 0.1, "high"},
		{u.OverHours, "over"},
		{u.OverHours + 100, "over"},
	}
	for _, c := range cases {
		if got := s.ClassifyUtilization(c.hours); got.Key != c.key {
			t.Errorf("%v h -> %q, want %q", c.hours, got.Key, c.key)
		}
	}
	// Unset thresholds must fall back to the defaults instead of classifying
	// everything as "over".
	bare := models.Settings{}
	if got := bare.ClassifyUtilization(30); got.Key != "optimal" {
		t.Errorf("unconfigured traffic light: 30 h -> %q, want optimal", got.Key)
	}
}

// --- carry-over and fiscal-year boundaries ---------------------------------

// An assignment that runs across the fiscal-year boundary splits exactly there,
// and the later year only gets the budget the earlier one left over.
func TestCarryOverSplitsAtTheBoundary(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-06-29", ProjectID: "old", Hours: 10}, // last week of FY 2026
			{Date: "2026-06-30", ProjectID: "old", Hours: 10},
			{Date: "2026-07-01", ProjectID: "old", Hours: 10}, // already FY 2027, booked on the old row
			{Date: "2026-09-15", ProjectID: "new", Hours: 30},
			{Date: "2027-07-01", ProjectID: "new", Hours: 5}, // spills into FY 2028
		},
	}
	ys := BuildYearSummary(d, holidays.New(2027, "BY"))
	if len(ys.Projects) != 1 {
		t.Fatalf("FY 2027 shows %d projects, want 1", len(ys.Projects))
	}
	ps := ys.Projects[0]
	if ps.CarryOver != 20 {
		t.Errorf("carry-over = %v, want 20 (the two June days)", ps.CarryOver)
	}
	if ps.Consumed != 40 {
		t.Errorf("consumed = %v, want 40 (01.07. + 15.09.)", ps.Consumed)
	}
	if ps.FutureFY != 5 {
		t.Errorf("future FY = %v, want 5", ps.FutureFY)
	}
	if ps.AvailableBudget != 380 {
		t.Errorf("available = %v, want 380", ps.AvailableBudget)
	}
	if ps.Remaining != 340 {
		t.Errorf("remaining = %v, want 340", ps.Remaining)
	}
	// The split must list every year that carries hours, ascending.
	wantSplit := []FYHours{
		{Year: 2026, Label: "FY 2026", Hours: 20, Past: true},
		{Year: 2027, Label: "FY 2027", Hours: 40, Current: true},
		{Year: 2028, Label: "FY 2028", Hours: 5},
	}
	if len(ps.FYSplit) != len(wantSplit) {
		t.Fatalf("FY split = %+v, want %+v", ps.FYSplit, wantSplit)
	}
	for i, w := range wantSplit {
		if ps.FYSplit[i] != w {
			t.Errorf("FY split[%d] = %+v, want %+v", i, ps.FYSplit[i], w)
		}
	}
	if !ps.SpansFY || !ys.HasFYSplit {
		t.Error("an assignment with hours in three fiscal years must be flagged as spanning")
	}
}

// The carry-over may never exceed the budget, so the available budget stays at
// zero instead of turning negative.
func TestCarryOverCannotExceedTheBudget(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "999", Name: "Over", BudgetHours: 50, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "999", Name: "Over", BudgetHours: 50, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-03-02", ProjectID: "old", Hours: 80},
			{Date: "2026-09-01", ProjectID: "new", Hours: 5},
		},
	}
	ps := BuildYearSummary(d, holidays.New(2027, "BY")).Projects[0]
	if ps.CarryOver != 50 {
		t.Errorf("carry-over = %v, want it capped at the 50 h budget", ps.CarryOver)
	}
	if ps.AvailableBudget != 0 {
		t.Errorf("available = %v, want 0", ps.AvailableBudget)
	}
	if ps.Remaining != -5 {
		t.Errorf("remaining = %v, want -5 (overbooked)", ps.Remaining)
	}
	if ps.PlannedPct != 100 {
		t.Errorf("planned = %v %%, want 100 with an exhausted budget", ps.PlannedPct)
	}
}

// --- known-bad documents ---------------------------------------------------

// Two project rows of the SAME fiscal year sharing one assignment ID pool their
// hours, so every row would report the full pooled amount and the fiscal-year
// roll-up would count them twice.
func TestDuplicateAssignmentInOneFiscalYearIsRejected(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "a", AssignmentID: "4711", Name: "Erste Hälfte", BudgetHours: 100, Active: true, FiscalYear: 2027},
			{ID: "b", AssignmentID: "4711", Name: "Zweite Hälfte", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-09-01", ProjectID: "a", Hours: 10},
			{Date: "2026-09-02", ProjectID: "b", Hours: 10},
		},
	}
	if err := models.Validate(d); err == nil {
		t.Fatal("Validate accepted two rows with the same assignment ID in one fiscal year")
	}
	// Different fiscal years are the normal continuation case and stay valid.
	d.Projects[1].FiscalYear = 2028
	if err := models.Validate(d); err != nil {
		t.Errorf("Validate rejected the same assignment in two fiscal years: %v", err)
	}
}

// Entries pointing at a project that no longer exists must not silently inflate
// the goal: the goal page and the year summary have to agree on what counts.
func TestOrphanEntriesDoNotCountTowardsTheGoal(t *testing.T) {
	year := currentFY(7)
	start, _ := FiscalYear(year, 7)
	iso := start.AddDate(0, 1, 0).Format("2006-01-02")

	d := models.Data{
		Settings: models.Settings{Year: year, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		FiscalYears: map[int]models.FiscalYearSettings{
			year: {VacationDays: 30},
		},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: year},
		},
		Entries: []models.Entry{
			{Date: iso, ProjectID: "p1", Hours: 8},
			{Date: iso, ProjectID: "ghost", Hours: 8}, // project was deleted
		},
	}
	cal := holidays.New(year, "BY")
	gs := BuildGoalSummary(d, cal)
	ys := BuildYearSummary(d, cal)

	if gs.Projected != ys.TotalHours {
		t.Errorf("goal counts %v h but the year summary counts %v h - orphan entries must be ignored",
			gs.Projected, ys.TotalHours)
	}
	flow := BuildGoalFlow(d, cal)
	if !eq(flow.Total, gs.Projected, 0.1) {
		t.Errorf("goal flow total %v != goal projection %v", flow.Total, gs.Projected)
	}
}

// Hours dated inside the reviewed fiscal year but booked on a project row of a
// DIFFERENT fiscal year still belong to this year by date. The goal, the flow
// and the weekly totals must agree on that.
func TestHoursOfAnotherYearsProjectRowStillCount(t *testing.T) {
	d := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		FiscalYears: map[int]models.FiscalYearSettings{
			2027: {VacationDays: 30},
		},
		Projects: []models.Project{
			{ID: "old", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2026},
			{ID: "new", AssignmentID: "5641245", Name: "Migration", BudgetHours: 400, Active: true, FiscalYear: 2027},
		},
		// Both dates are inside FY 2027 (01.07.2026 - 30.06.2027).
		Entries: []models.Entry{
			{Date: "2026-07-01", ProjectID: "old", Hours: 6},
			{Date: "2026-07-02", ProjectID: "new", Hours: 4},
		},
	}
	cal := holidays.New(2027, "BY")
	gs := BuildGoalSummary(d, cal)
	ys := BuildYearSummary(d, cal)
	flow := BuildGoalFlow(d, cal)

	if gs.Projected != 10 {
		t.Errorf("goal projection = %v, want 10", gs.Projected)
	}
	if ys.TotalHours != 10 {
		t.Errorf("year summary total = %v, want 10 (pooled per assignment)", ys.TotalHours)
	}
	if !eq(flow.Total, 10, 0.1) {
		t.Errorf("goal flow total = %v, want 10", flow.Total)
	}
	var weekSum float64
	for _, wt := range ys.WeekTotals {
		weekSum += wt.Hours
	}
	if !eq(weekSum, 10, 0.1) {
		t.Errorf("weekly totals = %v, want 10", weekSum)
	}
}

// --- inactive projects -----------------------------------------------------

// Deactivating a project releases only the budget that was never planned;
// booked and forecast hours stay untouched.
func TestInactiveReleasesOnlyUnplannedBudget(t *testing.T) {
	year := currentFY(7)
	start, _ := FiscalYear(year, 7)
	iso := start.AddDate(0, 1, 0).Format("2006-01-02")

	mk := func(active bool) ProjectSummary {
		d := models.Data{
			Settings: models.Settings{Year: year, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
			Projects: []models.Project{
				{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: active, FiscalYear: year},
			},
			Entries: []models.Entry{{Date: iso, ProjectID: "p1", Hours: 30}},
		}
		return BuildYearSummary(d, holidays.New(year, "BY")).Projects[0]
	}
	on, off := mk(true), mk(false)

	if on.Released != 0 || on.Remaining != 70 || on.AvailableBudget != 100 {
		t.Errorf("active: released/remaining/available = %v/%v/%v, want 0/70/100", on.Released, on.Remaining, on.AvailableBudget)
	}
	if off.Released != 70 || off.Remaining != 0 || off.AvailableBudget != 30 {
		t.Errorf("inactive: released/remaining/available = %v/%v/%v, want 70/0/30", off.Released, off.Remaining, off.AvailableBudget)
	}
	if off.Consumed != on.Consumed {
		t.Errorf("deactivating changed the consumed hours: %v -> %v", on.Consumed, off.Consumed)
	}
	if off.PlannedPct != 100 {
		t.Errorf("inactive planned = %v %%, want 100 (the remaining budget is gone)", off.PlannedPct)
	}
}

// --- week-to-date ----------------------------------------------------------

// The rate to date derives every figure from the same two numbers, so the
// relations between them must hold exactly.
func TestWeekToDateRelations(t *testing.T) {
	year := currentFY(7)
	d := genData(year, 7, 17)
	wtd := BuildWeekToDate(d, calFor(d))
	if !wtd.HasData {
		t.Skip("the fiscal year has not produced a full weekday yet")
	}
	if wtd.ElapsedWeeks <= 0 {
		t.Fatalf("elapsed weeks = %v", wtd.ElapsedWeeks)
	}
	if !eq(wtd.PerWeek, wtd.Hours/wtd.ElapsedWeeks, 0.2) {
		t.Errorf("PerWeek = %v, want %v", wtd.PerWeek, wtd.Hours/wtd.ElapsedWeeks)
	}
	if !eq(wtd.DeltaPerWeek, wtd.PerWeek-wtd.TargetPerWeek, 0.2) {
		t.Errorf("DeltaPerWeek = %v, want %v", wtd.DeltaPerWeek, wtd.PerWeek-wtd.TargetPerWeek)
	}
	if !eq(wtd.ExpectedHours, wtd.TargetPerWeek*wtd.ElapsedWeeks, 0.5) {
		t.Errorf("ExpectedHours = %v, want %v", wtd.ExpectedHours, wtd.TargetPerWeek*wtd.ElapsedWeeks)
	}
	if !eq(wtd.RatePct, wtd.PerWeek/wtd.TargetPerWeek*100, 0.5) {
		t.Errorf("RatePct = %v, want %v", wtd.RatePct, wtd.PerWeek/wtd.TargetPerWeek*100)
	}
	if wtd.FYWeeks != FYWeeks(year, 7) {
		t.Errorf("FYWeeks = %d, want %d", wtd.FYWeeks, FYWeeks(year, 7))
	}
	// Vacation never counts towards the goal rate.
	vacOnly := d
	vacOnly.Entries = nil
	for _, e := range d.Entries {
		if e.ProjectID == models.VacationProjectID(year) {
			vacOnly.Entries = append(vacOnly.Entries, e)
		}
	}
	if got := BuildWeekToDate(vacOnly, calFor(d)); got.Hours != 0 {
		t.Errorf("vacation-only year counts %v h towards the rate, want 0", got.Hours)
	}
}
