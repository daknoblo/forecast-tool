package docsite

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestBuildDemoDataIsValidAndDeterministic(t *testing.T) {
	today := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	d := buildDemoData(today)

	if err := models.Validate(d); err != nil {
		t.Fatalf("demo document is invalid: %v", err)
	}
	if !reflect.DeepEqual(d, buildDemoData(today)) {
		t.Fatal("demo data is not deterministic for the same day")
	}

	year := forecast.FiscalYearOf(today, demoStartMonth)
	current := models.ProjectsForFY(d.Projects, year)
	if len(current) != len(demoProjects)+1 { // + the automatic vacation project
		t.Fatalf("expected %d projects in FY %d, got %d", len(demoProjects)+1, year, len(current))
	}
	var vacation bool
	for _, p := range current {
		if p.IsVacation() {
			vacation = true
		}
	}
	if !vacation {
		t.Fatal("the vacation project is missing from the demo data")
	}

	// A continued assignment must exist in the previous fiscal year too, because
	// the carry-over columns are part of what the screenshots document.
	if len(models.ProjectsForFY(d.Projects, year-1)) == 0 {
		t.Fatalf("no carry-over project in FY %d", year-1)
	}

	// Hours must land on both sides of today so booked and forecast are visible.
	iso := today.Format("2006-01-02")
	var past, future bool
	for _, e := range d.Entries {
		switch {
		case e.Date < iso:
			past = true
		case e.Date > iso:
			future = true
		}
	}
	if !past || !future {
		t.Fatalf("expected booked and forecast hours, got past=%v future=%v", past, future)
	}
}

func TestRewritePointsLinksAtTheSnapshot(t *testing.T) {
	byURL := map[string]string{
		"/":      "index.html",
		"/goal":  "goal.html",
		"/month": "month.html",
	}

	html := `<html><head></head><body>` +
		`<link href="/static/style.css?v=abc123">` +
		`<a href="/">Dashboard</a>` +
		`<a href="/goal">Ziele</a>` +
		`<a href="/month?month=2026-07&amp;view=stored">Gespeichert</a>` +
		`<a href="/month?month=2026-07&amp;view=estimate">Schätzung</a>` +
		`<a href="/export">Export</a>` +
		`<a href="https://example.com">extern</a>` +
		`</body></html>`

	out, assets := rewrite(html, byURL)

	for _, want := range []string{
		`href="static/style.css"`,
		`href="index.html"`,
		`href="goal.html"`,
		`href="month.html"`,
		`href="#"`, // /export was not captured
		`href="https://example.com"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten page misses %s", want)
		}
	}
	if len(assets) != 1 || assets[0] != "/static/style.css?v=abc123" {
		t.Errorf("unexpected assets: %v", assets)
	}
	if !strings.Contains(out, "demo-banner") {
		t.Error("the demo banner was not injected")
	}
	if !strings.Contains(out, "window.fetch") {
		t.Error("the inert-demo script was not injected")
	}
}

func TestNormalizeURLSortsQuery(t *testing.T) {
	cases := map[string]string{
		"/":                                "/",
		"/month?view=stored&month=2026-07": "/month?month=2026-07&view=stored",
		"/?soff=1&sankey=fy":               "/?sankey=fy&soff=1",
	}
	for in, want := range cases {
		if got := normalizeURL(in); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDemoUsesMonthlyPlanningOnly(t *testing.T) {
	var monthPage, monthShot bool
	for _, page := range DemoPages() {
		if strings.Contains(page.URL, "/week") || strings.HasPrefix(page.File, "week") {
			t.Errorf("retired demo page: %+v", page)
		}
		monthPage = monthPage || page.URL == "/month"
	}
	for _, shot := range DemoShots() {
		if strings.Contains(shot.Path, "/week") || shot.File == "forecast.png" {
			t.Errorf("retired screenshot: %+v", shot)
		}
		monthShot = monthShot || shot.File == "month.png"
	}
	if !monthPage || !monthShot {
		t.Fatal("monthly demo page or screenshot missing")
	}
}
