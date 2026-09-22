package web

import (
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestProgressChartTargetCrossing(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name              string
		booked, projected []float64
		target, today     float64
		position          float64
		label             string
	}{
		{"future month", []float64{100, 180, 180}, []float64{100, 200, 340}, 300, 1.5, 2 + 100.0/140, "Ziel voraussichtlich am 22.09.2026 erreicht"},
		{"current forecast", []float64{100, 180, 180}, []float64{100, 200, 340}, 190, 1.5, 1.75, "Ziel voraussichtlich am 24.08.2026 erreicht"},
		{"current booked", []float64{100, 180, 180}, []float64{100, 200, 340}, 150, 1.5, 1.3125, "Ziel wurde am 10.08.2026 erreicht"},
		{"junction", []float64{100, 180, 180}, []float64{100, 200, 340}, 180, 1.5, 1.5, "Ziel wurde am 16.08.2026 erreicht"},
		{"before start", []float64{0, 0, 0}, []float64{100, 200, 300}, 50, -1, 0.5, "Ziel voraussichtlich am 16.07.2026 erreicht"},
		{"plateau", []float64{100, 100, 100}, []float64{100, 100, 100}, 100, 4, 1, "Ziel wurde am 01.08.2026 erreicht"},
		{"period end", []float64{0, 0, 0}, []float64{100, 200, 300}, 300, 0, 3, "Ziel voraussichtlich am 30.09.2026 erreicht"},
		{"not reached", []float64{0, 0, 0}, []float64{0, 0, 0}, 300, 1, -1, "Zielerreichung kann aktuell nicht geschätzt werden"},
		{"past uses booked", []float64{10, 20, 30}, []float64{100, 200, 300}, 200, 3, -1, "Zielerreichung kann aktuell nicht geschätzt werden"},
		{"no target", []float64{0, 0, 0}, []float64{100, 200, 300}, 0, 0, -1, ""},
	}
	marker := regexp.MustCompile(`<circle class="target-crossing" cx="([^"]+)" cy="([^"]+)"`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, wide := range []bool{false, true} {
				svg := string(progressSVG([]string{"Jul", "Aug", "Sep"}, tc.booked, tc.projected, tc.target, tc.today, wide, start))
				if tc.label == "" {
					if strings.Contains(svg, "target-crossing") {
						t.Fatal("annotation without target")
					}
					continue
				}
				if !strings.Contains(svg, ">"+tc.label+"</text>") {
					t.Fatalf("missing label %q", tc.label)
				}
				match := marker.FindStringSubmatch(svg)
				if tc.position < 0 {
					if match != nil {
						t.Fatal("marker without intersection")
					}
					continue
				}
				if match == nil {
					t.Fatal("missing crossing marker")
				}
				cx, _ := strconv.ParseFloat(match[1], 64)
				cy, _ := strconv.ParseFloat(match[2], 64)
				width := 560.0
				if wide {
					width = 1100
				}
				wantX := 48 + (width-48-46)*tc.position/3
				if math.Abs(cx-wantX) > 1e-8 {
					t.Fatalf("crossing x = %v, want %v", cx, wantX)
				}
				green, orange := curves(t, svg)
				onCurve := false
				for _, points := range [][][2]float64{green, orange} {
					for i := 1; i < len(points); i++ {
						a, b := points[i-1], points[i]
						if cx < a[0]-1e-8 || cx > b[0]+1e-8 || b[0] == a[0] {
							continue
						}
						yy := a[1] + (cx-a[0])/(b[0]-a[0])*(b[1]-a[1])
						onCurve = onCurve || math.Abs(cy-yy) < 1e-8
					}
				}
				if !onCurve {
					t.Fatal("marker is not on the rendered curve")
				}
			}
		})
	}
}

func TestProgressDateCalendarBoundaries(t *testing.T) {
	start := time.Date(2027, 12, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		pos  float64
		want string
	}{
		{0, "01.12.2027"},
		{1, "01.01.2028"},
		{2.5, "15.02.2028"},
		{3, "29.02.2028"},
	} {
		if got := progressDate(start, tc.pos, 3).Format("02.01.2006"); got != tc.want {
			t.Errorf("position %g: %s, want %s", tc.pos, got, tc.want)
		}
	}
}

func TestGoalChartsUseTheirPeriodStart(t *testing.T) {
	h, store := newTestServer(t)
	if err := store.Mutate(func(d *models.Data) error {
		d.Settings.Year, d.Settings.FiscalYearStartMonth = 2030, 7
		zero := 0
		d.FiscalYears[2030] = models.FiscalYearSettings{WeekdayHours: 120, HolidayDays: &zero}
		d.Projects = append(d.Projects, models.Project{
			ID: "crossing", AssignmentID: "crossing", Name: "Crossing", FiscalYear: 2030, Active: true,
		})
		start, _ := forecast.FiscalYear(2030, 7)
		for m := 0; m < 12; m++ {
			d.Entries = append(d.Entries, models.Entry{ProjectID: "crossing", Date: start.AddDate(0, m, 0).Format("2006-01-02"), Hours: 10})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/goal", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("goal status = %d", rec.Code)
	}
	labels := regexp.MustCompile(`<text class="target-crossing-label"[^>]*>([^<]+)</text>`).FindAllStringSubmatch(rec.Body.String(), -1)
	want := []string{"30.06.2030", "31.12.2029", "30.06.2030", "30.09.2029", "31.12.2029", "31.03.2030", "30.06.2030"}
	if len(labels) != len(want) {
		t.Fatalf("%d labels, want %d", len(labels), len(want))
	}
	for i, date := range want {
		if labels[i][1] != "Ziel voraussichtlich am "+date+" erreicht" {
			t.Errorf("chart %d: %q, want %s", i, labels[i][1], date)
		}
	}
}

// The two halves of the burn-up form ONE curve: the projection has to start
// exactly where the booked curve ends, whatever fraction of a sub-period today
// falls into.
func TestProgressChartHalvesMeetInOnePoint(t *testing.T) {
	labels := []string{"Jul", "Aug", "Sep", "Okt"}
	booked := []float64{100, 180, 180, 180}
	projected := []float64{100, 200, 280, 340}

	for _, todayPos := range []float64{0, 0.01, 0.5, 1, 1.3, 1.999, 2, 3.5, 4} {
		svg := string(progressSVG(labels, booked, projected, 300, todayPos, false, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)))
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
			svg := string(progressSVG([]string{"a", "b", "c"}, c.booked, c.projected, c.target, todayPos, false, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)))
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

	svg := string(progressSVG(labels, booked, projected, 300, 1.5, false, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)))
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
	svg = string(progressSVG(labels, []float64{100, 400, 400}, projected, 300, 3, false, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)))
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
	svg := string(progressSVG(labels, cumulative(act), cumulative(proj), gs.TargetHours, pos, true, start))

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
