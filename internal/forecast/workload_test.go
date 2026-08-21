package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

// workloadDoc builds a document whose only project is a plain, bookable one
// plus the fiscal year's vacation project.
func workloadDoc(t *testing.T, now time.Time) models.Data {
	t.Helper()
	year := FiscalYearOf(now, models.DefaultFiscalYearStartMonth)
	d := models.DefaultData(year)
	d.Settings.FederalState = "SN"
	d.Projects = append(d.Projects, models.Project{
		ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 5000,
		Color: "#2563eb", Active: true, FiscalYear: year,
	})
	models.EnsureVacationProject(&d, year)
	return d
}

// A Werktag runs from Monday to Saturday, so a regular 40 h week averages
// 40/6 h and stays well below the 8 h the law allows.
func TestWorkloadAveragesOverWerktage(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // a Monday
	d := workloadDoc(t, now)
	// Four full Mon-Fri weeks with 8 h each, ending the Friday before `now`.
	for day := now.AddDate(0, 0, -28); day.Before(now); day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		d.Entries = append(d.Entries, models.Entry{
			Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 8,
		})
	}

	w := buildWorkload(d, 1, now, false)
	if !w.HasData {
		t.Fatal("window has no data")
	}
	if w.PerDay >= WorkdayLimitHours {
		t.Errorf("PerDay = %v, want below the %v h limit", w.PerDay, WorkdayLimitHours)
	}
	if w.Hours <= 0 || w.Days <= 0 {
		t.Fatalf("hours = %v on %d Werktage", w.Hours, w.Days)
	}
	if want := round1(w.Hours / float64(w.Days)); w.PerDay != want {
		t.Errorf("PerDay = %v, want %v", w.PerDay, want)
	}
	if want := round1(float64(w.Days)*WorkdayLimitHours - w.Hours); w.Headroom != want {
		t.Errorf("Headroom = %v, want %v", w.Headroom, want)
	}
	if w.Over {
		t.Error("a 40 h week must not count as over the limit")
	}
}

// Saturdays are Werktage: booking one adds hours without adding a day, so the
// average can only rise.
func TestWorkloadCountsSaturdayAsWerktag(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	base := workloadDoc(t, now)
	for day := now.AddDate(0, 0, -28); day.Before(now); day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		base.Entries = append(base.Entries, models.Entry{
			Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 8,
		})
	}
	plain := buildWorkload(base, 1, now, false)

	withSat := base
	withSat.Entries = append(append([]models.Entry(nil), base.Entries...),
		models.Entry{Date: now.AddDate(0, 0, -2).Format("2006-01-02"), ProjectID: "p1", Hours: 6})
	sat := buildWorkload(withSat, 1, now, false)

	if sat.Days != plain.Days {
		t.Errorf("Werktage = %d with Saturday hours, want %d - Saturdays always count", sat.Days, plain.Days)
	}
	if sat.PerDay <= plain.PerDay {
		t.Errorf("PerDay = %v with Saturday hours, want more than %v", sat.PerDay, plain.PerDay)
	}
}

// Vacation is not working time. A vacation week must leave the average of the
// days actually worked untouched instead of diluting it.
func TestWorkloadNeutralisesVacation(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	vacationID := models.VacationProjectID(d.Settings.Year)
	for day := now.AddDate(0, 0, -28); day.Before(now); day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		// The most recent week is vacation, the ones before it are worked.
		if !day.Before(now.AddDate(0, 0, -7)) {
			d.Entries = append(d.Entries, models.Entry{
				Date: day.Format("2006-01-02"), ProjectID: vacationID, Hours: 8,
			})
			continue
		}
		d.Entries = append(d.Entries, models.Entry{
			Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 9,
		})
	}

	w := buildWorkload(d, 1, now, false)
	if w.Hours != 135 { // 15 worked days x 9 h
		t.Errorf("Hours = %v, want 135 - vacation hours must not count", w.Hours)
	}
	// The window covers four weeks: 15 worked days plus 4 Saturdays; the five
	// vacation days drop out.
	if w.Days != 19 {
		t.Errorf("Werktage = %d, want 19 (15 worked + 4 Saturdays)", w.Days)
	}
}

// The daily cap of the law is separate from the average: a day above 10 h is
// reported even while the window as a whole stays inside the limit.
func TestWorkloadReportsLongDaysAndPeak(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	d.Entries = append(d.Entries,
		models.Entry{Date: now.AddDate(0, 0, -7).Format("2006-01-02"), ProjectID: "p1", Hours: 11},
		models.Entry{Date: now.AddDate(0, 0, -6).Format("2006-01-02"), ProjectID: "p1", Hours: 10},
		models.Entry{Date: now.AddDate(0, 0, -5).Format("2006-01-02"), ProjectID: "p1", Hours: 12.5},
	)

	w := buildWorkload(d, 1, now, false)
	if w.LongDays != 2 {
		t.Errorf("LongDays = %d, want 2 - exactly %v h is still allowed", w.LongDays, LongDayHours)
	}
	if w.PeakHours != 12.5 {
		t.Errorf("PeakHours = %v, want 12.5", w.PeakHours)
	}
	if w.Over {
		t.Error("three long days in a month must not tip the monthly average over the limit")
	}
}

// Today is still running, and entries of a deleted project are ignored by every
// roll-up - this one included.
func TestWorkloadSkipsTodayAndUnknownProjects(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	d.Entries = append(d.Entries,
		models.Entry{Date: yesterday, ProjectID: "p1", Hours: 7},
		models.Entry{Date: now.Format("2006-01-02"), ProjectID: "p1", Hours: 99},
		models.Entry{Date: yesterday, ProjectID: "gone", Hours: 99},
	)

	if w := buildWorkload(d, 1, now, false); w.Hours != 7 {
		t.Errorf("Hours = %v, want 7", w.Hours)
	}
}

// An empty window has to report itself as empty so the UI can show a
// placeholder instead of a meaningless 0 h average.
func TestWorkloadWithoutHoursHasNoData(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	if w := buildWorkload(workloadDoc(t, now), 6, now, false); w.HasData {
		t.Errorf("empty window reports data: %+v", w)
	}
	if w := buildWorkload(workloadDoc(t, now), 0, now, false); w.HasData {
		t.Error("a zero-month window reports data")
	}
}

// The forward window starts today - hours from today on are forecast - and must
// see the plan while the backward window sees none of it.
func TestWorkloadAheadMeasuresThePlan(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	for i := 0; i < 14; i++ {
		day := now.AddDate(0, 0, i)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		d.Entries = append(d.Entries, models.Entry{
			Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 9,
		})
	}
	// Yesterday is booked, so only the backward window may see it.
	d.Entries = append(d.Entries, models.Entry{
		Date: now.AddDate(0, 0, -1).Format("2006-01-02"), ProjectID: "p1", Hours: 4,
	})

	ahead := buildWorkload(d, 1, now, true)
	if !ahead.Ahead {
		t.Error("forward window is not flagged as such")
	}
	if ahead.Hours != 90 { // 10 planned weekdays x 9 h
		t.Errorf("planned hours = %v, want 90", ahead.Hours)
	}
	if ahead.StartLabel != now.Format("02.01.2006") {
		t.Errorf("forward window starts on %s, want today", ahead.StartLabel)
	}
	if ahead.Filled != 10 {
		t.Errorf("Filled = %d, want 10 - only the planned Werktage carry hours", ahead.Filled)
	}
	if ahead.Filled >= ahead.Days {
		t.Errorf("Filled = %d of %d Werktage; the rest of the month must stay unplanned", ahead.Filled, ahead.Days)
	}
	// Two intense weeks spread over a month still average out below the limit -
	// which is exactly what the forward view is there to show.
	if ahead.Over {
		t.Errorf("PerDay = %v is reported as over the limit", ahead.PerDay)
	}

	if back := buildWorkload(d, 1, now, false); back.Hours != 4 {
		t.Errorf("backward window sees %v h, want only yesterday's 4 h", back.Hours)
	}
}

// A forward window that reaches past the planning horizon must stop there:
// months nobody has planned yet are not months without work, and counting them
// would average an overloaded plan back into the green.
func TestWorkloadAheadSkipsUnplannedMonths(t *testing.T) {
	now := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	// March is planned to the brim, April to August are untouched.
	for day := now; day.Month() == time.March; day = day.AddDate(0, 0, 1) {
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		d.Entries = append(d.Entries, models.Entry{
			Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 11,
		})
	}

	month := buildWorkload(d, 1, now, true)
	half := buildWorkload(d, 6, now, true)
	if !month.HasData || !half.HasData {
		t.Fatal("planned month is not reported")
	}
	// The window runs from 02.03. to 01.09., so it touches seven calendar
	// months; the six after March carry nothing.
	if half.Skipped != 6 {
		t.Errorf("Skipped = %d, want the 6 unplanned months", half.Skipped)
	}
	if half.Days != month.Days || half.PerDay != month.PerDay {
		t.Errorf("6-month window = %v h on %d days, want the same as the 1-month one (%v h on %d)",
			half.PerDay, half.Days, month.PerDay, month.Days)
	}
	if !half.Over {
		t.Errorf("PerDay = %v; an 11 h plan has to show up as over the limit", half.PerDay)
	}
	if half.EndLabel != "31.03.2026" {
		t.Errorf("EndLabel = %q, want the last planned day", half.EndLabel)
	}

	// The backward window keeps every month: not having worked is a fact, not a
	// gap in the plan.
	if back := buildWorkload(d, 6, now, false); back.Skipped != 0 {
		t.Errorf("backward window skipped %d months, want 0", back.Skipped)
	}
}

// The timeline puts today in the middle: a fixed history to the left and only
// as much future as is planned to the right, capped at WorkloadAheadMonths.
func TestWorkloadTimelineSpansTodayAndStopsAtTheHorizon(t *testing.T) {
	now := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	// Two months of history and two months of plan.
	for i := -60; i < 60; i++ {
		day := now.AddDate(0, 0, i)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		d.Entries = append(d.Entries, models.Entry{
			Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 8,
		})
	}

	tl := buildWorkloadTimeline(d, now)
	if !tl.HasData {
		t.Fatal("timeline has no data")
	}
	// Six months back plus March, then April and May from the plan.
	if got, want := len(tl.Months), WorkloadBackMonths+3; got != want {
		t.Errorf("%d months, want %d (%s – %s)", got, want, tl.StartLabel, tl.EndLabel)
	}
	if tl.StartLabel != "01.09.2025" || tl.EndLabel != "31.05.2026" {
		t.Errorf("range = %s – %s, want 01.09.2025 – 31.05.2026", tl.StartLabel, tl.EndLabel)
	}
	if tl.HorizonLabel != "14.05.2026" {
		t.Errorf("HorizonLabel = %q, want the last planned day", tl.HorizonLabel)
	}

	cur := tl.Months[WorkloadBackMonths]
	if !cur.Current || cur.Ahead {
		t.Errorf("March is %+v, want the current month", cur)
	}
	if pos := float64(WorkloadBackMonths) + 15.0/31.0; math.Abs(tl.TodayPos-pos) > 0.001 {
		t.Errorf("TodayPos = %v, want %v", tl.TodayPos, pos)
	}
	for i, m := range tl.Months {
		if want := i > WorkloadBackMonths; m.Ahead != want {
			t.Errorf("month %d (%s): Ahead = %v, want %v", i, m.Label, m.Ahead, want)
		}
	}
	// A month before the bookings started stays empty instead of reporting a
	// zero average.
	if first := tl.Months[0]; first.HasData {
		t.Errorf("September reports data: %+v", first)
	}
}

// Without any plan the timeline stops at the end of the current month; an empty
// year to the right would only stretch the axis.
func TestWorkloadTimelineWithoutPlanEndsWithTheCurrentMonth(t *testing.T) {
	now := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	d := workloadDoc(t, now)
	d.Entries = append(d.Entries, models.Entry{
		Date: now.AddDate(0, 0, -1).Format("2006-01-02"), ProjectID: "p1", Hours: 8,
	})

	tl := buildWorkloadTimeline(d, now)
	if tl.HorizonLabel != "" {
		t.Errorf("HorizonLabel = %q, want empty", tl.HorizonLabel)
	}
	if got, want := len(tl.Months), WorkloadBackMonths+1; got != want {
		t.Errorf("%d months, want %d", got, want)
	}
	if tl.EndLabel != "31.03.2026" {
		t.Errorf("EndLabel = %q, want the end of the current month", tl.EndLabel)
	}
}
