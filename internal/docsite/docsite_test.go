package docsite

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if !strings.Contains(out, "form-action 'none'") {
		t.Error("the inert-demo content policy was not injected")
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<nav><a href="/">Dashboard</a><a href="/month">Monthly planning</a></nav>`))
	}))
	defer server.Close()
	pages, err := DiscoverDemoPages(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var monthPage, monthShot bool
	for _, page := range pages {
		if strings.Contains(page.URL, "/week") || strings.HasPrefix(page.File, "week") {
			t.Errorf("retired demo page: %+v", page)
		}
		monthPage = monthPage || page.URL == "/month"
	}
	for _, shot := range DemoShots(pages) {
		if strings.Contains(shot.Path, "/week") || shot.File == "forecast.png" {
			t.Errorf("retired screenshot: %+v", shot)
		}
		monthShot = monthShot || shot.File == "month.png"
	}
	if !monthPage || !monthShot {
		t.Fatal("monthly demo page or screenshot missing")
	}
}

func TestNavigationDiscoveryIsBoundedAndSafe(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/reports" {
			_, _ = w.Write([]byte(`<nav><a href="/reports/detail">Detail</a></nav>`))
			return
		}
		_, _ = w.Write([]byte(`<nav>
		<a href="/reports#summary">Reports <span>&amp; totals</span></a>
		<a href="/reports?view=fy&amp;sort=name">Annual</a>
		<a href="/reports?sort=name&amp;view=fy">Duplicate</a>
		<a href="/export">Export</a><a href="/api/v1/data">API</a>
		<a href="/download" download>Download</a><a href="/private">Private</a>
		<a href="//example.com">External</a><a href="https://example.com/">External</a>
		<a href="/%2e%2e/secret">Encoded traversal</a><a href="/../secret">Traversal</a>
		<a href="javascript:alert(1)">Script</a></nav>
		<a href="/unbounded?month=3000-01">Content link</a>
		<form action="/save"><button>Save</button></form>`))
	}))
	defer server.Close()
	pages, err := DiscoverDemoPages(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 11 { // home, reports, query variant, detail, seven horizons
		t.Fatalf("unexpected pages: %+v", pages)
	}
	if pages[1].Title != "Reports & totals" {
		t.Fatalf("title: %q", pages[1].Title)
	}
	for _, requested := range requests {
		if requested != "/" && requested != "/reports" && requested != "/reports/detail" {
			t.Errorf("unsafe request: %s", requested)
		}
	}
	shots := DemoShots(pages)
	for _, page := range pages {
		var captured bool
		for _, shot := range shots {
			captured = captured || shot.Path == page.URL
		}
		if !captured {
			t.Errorf("no screenshot for %s", page.URL)
		}
	}
}

func TestNavigationRejectsRedirectAndNonHTML(t *testing.T) {
	for _, mode := range []string{"redirect", "json", "error", "collision", "limit"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, "https://example.com", http.StatusFound)
				case "json":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{}`))
				case "error":
					http.Error(w, "broken", http.StatusInternalServerError)
				case "collision":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte(`<nav><a href="/a/b">A</a><a href="/a--b">B</a></nav>`))
				case "limit":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte(`<nav><a href="` + r.URL.Path + `a">Next</a></nav>`))
				}
			}))
			defer server.Close()
			if _, err := DiscoverDemoPages(server.URL); err == nil {
				t.Fatal("expected discovery error")
			}
		})
	}
}

func TestSnapshotPreservesFragmentsAndDisablesActions(t *testing.T) {
	input := `<html><head><script>fetch('https://example.com')</script></head><body>
		<a href="/settings#ai">AI</a><a href="/month?month=2026-07#fyw-2">Week</a>
		<form action="/settings"><input name="year"><button>Save</button></form>
		<script>doWork()</script></body></html>`
	out, _ := rewrite(input, map[string]string{"/settings": "settings.html", "/month": "month.html"})
	for _, want := range []string{`href="settings.html#ai"`, `href="month.html#fyw-2"`, `action="#"`, `<input disabled`, `<button disabled`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "doWork") {
		t.Error("snapshot retains executable scripts")
	}
}

func writeFixture(t *testing.T, root, file, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMarkdownDiscoveryAndBuild(t *testing.T) {
	repo, out := t.TempDir(), t.TempDir()
	writeFixture(t, repo, "README.md", "# Project\n[Start](docs/API.md#usage)\n[Title](docs/API.md#api)\n")
	writeFixture(t, repo, "docs/API.md", "# API\n## Usage\n[Nested](guides/New.md?view=all#details)\n")
	writeFixture(t, repo, "docs/guides/New.md", "# Newly added guide\n## Details\n[API](../API.md#usage)\n[Home](../../README.md)\n[Self](#details)\n[Source](../../go.mod)\n![Diagram](../image.svg)\n")
	writeFixture(t, repo, "docs/scout-automation.example.json", `{"untouched":true}`)
	if err := BuildSite(repo, out, nil, nil, "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	for file, wants := range map[string][]string{
		"index.html":      {`href="api.html#usage"`, `href="guides/new.html"`, `href="api.html#api"`},
		"api.html":        {`href="guides/new.html?view=all#details"`, `id="usage"`, `<h1 id="api">API</h1>`},
		"guides/new.html": {`href="../api.html#usage"`, `href="../index.html"`, `href="#details"`, `href="../assets/site.css"`, `href="` + RepoURL + `/blob/main/go.mod"`, `src="https://raw.githubusercontent.com/daknoblo/forecast-tool/main/docs/image.svg"`, `lang="en"`},
	} {
		content, err := os.ReadFile(filepath.Join(out, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(content), want) {
				t.Errorf("%s misses %s", file, want)
			}
		}
	}
}

func TestMarkdownDiscoveryFailures(t *testing.T) {
	for _, mode := range []string{"collision", "reserved", "symlink", "heading", "broken-link", "escape"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			writeFixture(t, repo, "README.md", "# Project\n")
			writeFixture(t, repo, "docs/Guide.md", "# Guide\n")
			switch mode {
			case "collision":
				writeFixture(t, repo, "docs/GUIDE.MD", "# Duplicate\n")
				first, err := os.Stat(filepath.Join(repo, "docs/Guide.md"))
				if err != nil {
					t.Fatal(err)
				}
				second, err := os.Stat(filepath.Join(repo, "docs/GUIDE.MD"))
				if err != nil {
					t.Fatal(err)
				}
				if os.SameFile(first, second) {
					t.Skip("case-insensitive filesystem cannot represent a case collision")
				}
			case "reserved":
				writeFixture(t, repo, "docs/screenshots.md", "# Reserved\n")
			case "symlink":
				if err := os.Symlink(filepath.Join(repo, "README.md"), filepath.Join(repo, "docs", "link.md")); err != nil {
					t.Fatal(err)
				}
			case "heading":
				writeFixture(t, repo, "docs/no-heading.md", "No heading\n")
			case "broken-link":
				writeFixture(t, repo, "docs/Guide.md", "# Guide\n[Missing](missing.md#section)\n")
			case "escape":
				writeFixture(t, repo, "docs/Guide.md", "# Guide\n[Escape](../../secret)\n")
			}
			if err := BuildSite(repo, t.TempDir(), nil, nil, "today"); err == nil {
				t.Fatal("expected build failure")
			}
		})
	}
}

func TestDocLinksPreserveURLComponents(t *testing.T) {
	page := markdownPage{Source: "docs/guides/Intro.md", File: "guides/intro.html"}
	links := map[string]string{"README.md": "index.html", "docs/API.md": "api.html"}
	cases := map[string]string{
		`../API.md?view=all&amp;x=1#usage`:        `../api.html?view=all&amp;x=1#usage`,
		`../../README.md`:                         `../index.html`,
		`../scout-automation.example.json`:        RepoURL + `/blob/main/docs/scout-automation.example.json`,
		`#same-page`:                              `#same-page`,
		`mailto:user@example.com`:                 `mailto:user@example.com`,
		`//example.com/path`:                      `//example.com/path`,
		`https://example.com/a.md#b`:              `https://example.com/a.md#b`,
		pagesPrefix + `screenshots.html#goal.png`: `../screenshots.html#goal.png`,
	}
	for input, want := range cases {
		out, err := rewriteDocLinks(`<a href="`+input+`">Link</a>`, page, links)
		if err != nil {
			t.Fatal(err)
		}
		if out != `<a href="`+want+`">Link</a>` {
			t.Errorf("%s: got %s", input, out)
		}
	}
}

func TestValidateSiteLinksAndAnchors(t *testing.T) {
	for _, failure := range []string{"", "file", "anchor", "image", "escape"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "index.html", `<a href="guides/new.html#details">Guide</a><img src="screenshots/home.png">`)
			writeFixture(t, root, "guides/new.html", `<h2 id="details">Details</h2><a href="../index.html">Home</a>`)
			writeFixture(t, root, "screenshots/home.png", "test image")
			switch failure {
			case "file":
				writeFixture(t, root, "index.html", `<a href="missing.html">Broken</a>`)
			case "anchor":
				writeFixture(t, root, "index.html", `<a href="guides/new.html#missing">Broken</a>`)
			case "image":
				writeFixture(t, root, "index.html", `<img src="screenshots/missing.png">`)
			case "escape":
				writeFixture(t, root, "index.html", `<a href="../secret">Outside</a>`)
			}
			err := ValidateSite(root, true)
			if (err != nil) != (failure != "") {
				t.Fatalf("validation result: %v", err)
			}
			if failure == "image" {
				if err := ValidateSite(root, false); err != nil {
					t.Fatalf("deliberate screenshot-free build: %v", err)
				}
			}
		})
	}
}
