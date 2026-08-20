package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

var (
	polylineRe = regexp.MustCompile(`<polyline fill="none" stroke="(#[0-9a-f]{6})" stroke-width="2\.5"(?: stroke-dasharray="5 3")? points="([^"]+)"`)
	pillRe     = regexp.MustCompile(`<text x="[\d.]+" y="14" font-size="11" font-weight="600" fill="#ffffff" text-anchor="middle">([^<]+)</text>`)
)

// chartPoints parses the "x,y x,y ..." points of a polyline.
func chartPoints(t *testing.T, s string) [][2]float64 {
	t.Helper()
	fields := strings.Fields(s)
	out := make([][2]float64, 0, len(fields))
	for _, f := range fields {
		xy := strings.SplitN(f, ",", 2)
		if len(xy) != 2 {
			t.Fatalf("malformed point %q", f)
		}
		x, err1 := strconv.ParseFloat(xy[0], 64)
		y, err2 := strconv.ParseFloat(xy[1], 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("malformed point %q", f)
		}
		out = append(out, [2]float64{x, y})
	}
	return out
}

// curves returns the booked (green) and projection (orange) polylines.
func curves(t *testing.T, svg string) (booked, projected [][2]float64) {
	t.Helper()
	for _, m := range polylineRe.FindAllStringSubmatch(svg, -1) {
		pts := chartPoints(t, m[2])
		switch m[1] {
		case "#16a34a":
			booked = pts
		case "#ea580c":
			projected = pts
		}
	}
	return booked, projected
}

// The two halves of the burn-up form ONE curve: the projection has to start
// exactly where the booked curve ends, whatever fraction of a sub-period today
// falls into.
func TestProgressChartHalvesMeetInOnePoint(t *testing.T) {
	labels := []string{"Jul", "Aug", "Sep", "Okt"}
	booked := []float64{100, 180, 180, 180}
	projected := []float64{100, 200, 280, 340}

	for _, todayPos := range []float64{0, 0.01, 0.5, 1, 1.3, 1.999, 2, 3.5, 4} {
		svg := string(progressSVG(labels, booked, projected, 300, todayPos, false))
		green, orange := curves(t, svg)
		if todayPos > 0 && len(green) == 0 {
			t.Fatalf("todayPos=%v: no booked curve", todayPos)
		}
		if todayPos < 4 && len(orange) == 0 {
			t.Fatalf("todayPos=%v: no projection", todayPos)
		}
		if len(green) > 0 && len(orange) > 0 {
			end, start := green[len(green)-1], orange[0]
			if math.Abs(end[0]-start[0]) > 0.001 || math.Abs(end[1]-start[1]) > 0.001 {
				t.Errorf("todayPos=%v: curve breaks at %v -> %v", todayPos, end, start)
			}
		}
		// The booked curve must start at zero hours (bottom of the plot).
		if len(green) > 0 {
			if x := green[0][0]; math.Abs(x-48) > 0.001 {
				t.Errorf("todayPos=%v: booked curve starts at x=%v, want the y axis", todayPos, x)
			}
		}
	}
}

// Nothing may be drawn outside the plot box, and the axis must be able to hold
// the highest value of either curve as well as the target.
func TestProgressChartStaysInsideThePlot(t *testing.T) {
	cases := []struct {
		booked, projected []float64
		target            float64
	}{
		{[]float64{10, 20, 30}, []float64{10, 40, 90}, 50},
		{[]float64{500, 900, 900}, []float64{500, 950, 1000}, 100}, // way over target
		{[]float64{0, 0, 0}, []float64{0, 0, 0}, 400},              // nothing planned
		{[]float64{1, 2, 3}, []float64{1, 2, 3}, 0},                // no target at all
		{[]float64{0.1, 0.2, 0.3}, []float64{0.1, 0.4, 0.9}, 0.5},  // tiny values
		{[]float64{10, 20, 30}, []float64{10, 20, 30}, 100000},     // huge target
	}
	const padT, plotBottom = 30.0, 30.0 + 162.0
	for _, c := range cases {
		for _, todayPos := range []float64{0, 1.5, 3} {
			svg := string(progressSVG([]string{"a", "b", "c"}, c.booked, c.projected, c.target, todayPos, false))
			green, orange := curves(t, svg)
			for _, pts := range [][][2]float64{green, orange} {
				for _, p := range pts {
					if p[1] < padT-0.001 || p[1] > plotBottom+0.001 {
						t.Errorf("target=%v todayPos=%v: point %v leaves the plot [%v..%v]",
							c.target, todayPos, p, padT, plotBottom)
					}
				}
			}
		}
	}
}

// The pills above the plot are the chart's legend and must agree with the data:
// Forecast is the projection minus what is already booked, never negative.
func TestProgressChartPillsMatchTheData(t *testing.T) {
	labels := []string{"Jul", "Aug", "Sep"}
	booked := []float64{100, 180, 180}
	projected := []float64{100, 200, 340}

	svg := string(progressSVG(labels, booked, projected, 300, 1.5, false))
	got := pillRe.FindAllStringSubmatch(svg, -1)
	want := []string{"Gebucht 180 h", "Forecast 160 h", "Hochrechnung 340 h", "Ziel 300 h"}
	if len(got) != len(want) {
		t.Fatalf("%d pills, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i][1] != w {
			t.Errorf("pill %d = %q, want %q", i, got[i][1], w)
		}
	}

	// A projection below the booked total (data entered backwards) must not
	// produce a negative forecast pill.
	svg = string(progressSVG(labels, []float64{100, 400, 400}, projected, 300, 3, false))
	for _, m := range pillRe.FindAllStringSubmatch(svg, -1) {
		if strings.Contains(m[1], "-") {
			t.Errorf("pill %q shows a negative value", m[1])
		}
	}
}

// niceStep has to produce an axis that can actually hold the peak, with a step
// from the 1/2/2.5/5 family.
func TestNiceStepCoversThePeak(t *testing.T) {
	for _, peak := range []float64{0.3, 1, 7, 42, 359, 646, 1436, 98765} {
		step := niceStep(peak, 4)
		if step <= 0 {
			t.Fatalf("peak=%v: step = %v", peak, step)
		}
		yMax := math.Ceil(peak/step) * step
		if yMax < peak-1e-9 {
			t.Errorf("peak=%v: axis ends at %v", peak, yMax)
		}
		if n := yMax / step; n > 6 {
			t.Errorf("peak=%v: %v gridlines, too crowded", peak, n)
		}
	}
	if got := niceStep(0, 4); got <= 0 {
		t.Errorf("niceStep(0) = %v, want a positive fallback", got)
	}
	if got := niceStep(-5, 4); got <= 0 {
		t.Errorf("niceStep(-5) = %v, want a positive fallback", got)
	}
}

// The goal charts are fed from the goal summary, so the curve's last value has
// to be the projection the page reports in its KPIs.
func TestGoalChartsMatchTheGoalSummary(t *testing.T) {
	h, store := newTestServer(t)
	_ = h
	d := store.Snapshot()
	year := d.Settings.Year
	start, _ := forecast.FiscalYear(year, d.Settings.FiscalYearStartMonth)

	if err := store.Mutate(func(d *models.Data) error {
		d.FiscalYears[year] = models.FiscalYearSettings{VacationDays: 30}
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 900,
			Color: "#2563eb", Active: true, FiscalYear: year,
		})
		for i := 0; i < 200; i++ {
			day := start.AddDate(0, 0, i)
			if wd := day.Weekday(); wd.String() == "Saturday" || wd.String() == "Sunday" {
				continue
			}
			d.Entries = append(d.Entries, models.Entry{Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: 4.5})
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap := store.Snapshot()
	gs := forecast.BuildGoalSummary(snap, holidays.Get(year, snap.Settings.FederalState))
	if len(gs.Months) != 12 {
		t.Fatalf("%d months", len(gs.Months))
	}
	labels := make([]string, 12)
	proj := make([]float64, 12)
	act := make([]float64, 12)
	for i, m := range gs.Months {
		labels[i], proj[i], act[i] = m.Label, m.Projected, m.Actual
	}
	pos := forecast.FYMonthProgress(year, snap.Settings.FiscalYearStartMonth)
	svg := string(progressSVG(labels, cumulative(act), cumulative(proj), gs.TargetHours, pos, true))

	pills := pillRe.FindAllStringSubmatch(svg, -1)
	if len(pills) != 4 {
		t.Fatalf("%d pills, want 4", len(pills))
	}
	if want := "Gebucht " + formatHours(gs.ActualTotal) + " h"; pills[0][1] != want {
		t.Errorf("booked pill = %q, want %q", pills[0][1], want)
	}
	if want := "Hochrechnung " + formatHours(gs.Projected) + " h"; pills[2][1] != want {
		t.Errorf("projection pill = %q, want %q", pills[2][1], want)
	}
	if want := "Ziel " + formatHours(gs.TargetHours) + " h"; pills[3][1] != want {
		t.Errorf("target pill = %q, want %q", pills[3][1], want)
	}
}

// cumulative must be a running sum that never loses hours to rounding drift.
func TestCumulativeIsARunningSum(t *testing.T) {
	in := []float64{1.15, 2.25, 0, 3.35, 10}
	got := cumulative(in)
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	var run float64
	for i, v := range in {
		run += v
		if math.Abs(got[i]-run) > 0.06 {
			t.Errorf("cumulative[%d] = %v, want ~%v", i, got[i], run)
		}
		if i > 0 && got[i] < got[i-1] {
			t.Errorf("cumulative decreases at %d: %v -> %v", i, got[i-1], got[i])
		}
	}
	if len(cumulative(nil)) != 0 {
		t.Error("cumulative(nil) must stay empty")
	}
}
