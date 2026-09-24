package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestMonthRouteReadOnlyAndPrivate(t *testing.T) {
	h, store := newTestServer(t)
	if err := store.Mutate(func(d *models.Data) error {
		d.Settings.Year = 2027
		d.Settings.FiscalYearStartMonth = 7
		d.Projects = append(d.Projects, models.Project{
			ID: "private-project", Name: "SensitiveProject", AssignmentID: "secret",
			FiscalYear: 2027, Active: true, Color: "#123456",
		})
		d.Entries = append(d.Entries, models.Entry{Date: "2026-07-02", ProjectID: "private-project", Hours: 8})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	for _, tc := range []struct {
		path    string
		private bool
		status  int
		want    string
	}{
		{"/month?month=2026-07&view=stored", false, 200, "SensitiveProject"},
		{"/month?month=2026-07", false, 200, "lokale Schätzung ab heute"},
		{"/month?month=2026-07", true, 200, "frei erfundene Beispieldaten"},
		{"/month?month=2020-01", false, 200, "Juli 2026"},
		{"/month?month=2026-13", false, 400, "Ungültiger Monat"},
		{"/month?view=wrong", false, 200, "lokale Schätzung ab heute"},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.private {
			req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != tc.status || !strings.Contains(body, tc.want) {
			t.Errorf("%s: status=%d, missing %q", tc.path, rec.Code, tc.want)
		}
		if tc.private && strings.Contains(body, "SensitiveProject") {
			t.Fatal("real project exposed in private mode")
		}
		if tc.status == 200 && !strings.Contains(body, `href="/month" class="active">Monatsplanung</a>`) {
			t.Fatal("active calendar navigation missing")
		}
		if tc.status == 200 && (strings.Contains(body, "<thead>") || strings.Contains(body, "month-modes") || strings.Contains(body, "&amp;view=")) {
			t.Fatal("calendar must not have a weekday header or view switch")
		}
		if tc.status == 200 && (strings.Contains(body, "month-readonly") || strings.Contains(body, "Projektstunden als Tagesblöcke")) {
			t.Fatal("removed monthly header subtitles still appear")
		}
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("GET calendar mutated stored data")
	}
}

func TestMonthLoadingIndicator(t *testing.T) {
	b, err := os.ReadFile("templates/month.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"var endActivity = window.beginActivity();", "endActivity();",
		"generate.setAttribute('aria-busy', 'true');", "generate.removeAttribute('aria-busy');",
		"if (planningError) planningError.hidden = true;",
	} {
		if !strings.Contains(string(b), required) {
			t.Fatalf("missing loading lifecycle: %s", required)
		}
	}
	if strings.Contains(string(b), "show('KI plant den Monat.") || strings.Contains(string(b), "month-loading") {
		t.Fatal("generation still displays a local loading indicator")
	}
}

func TestMonthTemplateSeparatesGenerationAndSave(t *testing.T) {
	b, err := os.ReadFile("templates/month.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`id="month-generate"`, `action="/month/save"`, `name="preview"`, `action="/month/prompt"`, `id="month-prompt"`} {
		if !strings.Contains(string(b), required) {
			t.Fatalf("calendar missing explicit planning control: %s", required)
		}
	}
}

func TestMonthWeeklySummaryLayout(t *testing.T) {
	_, store := newTestServer(t)
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := models.DefaultData(2026)
	d.Settings.FiscalYearStartMonth = 1
	d.Projects = []models.Project{
		{ID: "p", Name: "Projekt Alpha", Color: "#123456", FiscalYear: 2026},
		{ID: "q", Name: "Projekt Beta", Color: "#654321", FiscalYear: 2026},
		{ID: "v", Name: "Urlaub", System: models.VacationSystem, FiscalYear: 2026, Color: "#32cd32"},
	}
	d.Entries = []models.Entry{
		{Date: "2026-08-31", ProjectID: "p", Hours: 30},
		{Date: "2026-09-01", ProjectID: "q", Hours: 12},
		{Date: "2026-09-02", ProjectID: "v", Hours: 8},
	}
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	plan := forecast.BuildStoredMonthPlan(d, holidays.Get(2026, "SN"), now, now)
	rec := httptest.NewRecorder()
	srv.render(rec, httptest.NewRequest("GET", "/month", nil), "month.html", map[string]any{
		"Settings": d.Settings, "FYYears": []int{2026}, "Plan": plan, "Active": "month",
	})
	weeks := regexp.MustCompile(`(?s)<th scope="row" class="month-week">(.*?)</th>`).FindAllStringSubmatch(rec.Body.String(), -1)
	if rec.Code != 200 || len(weeks) != len(plan.Weeks) {
		t.Fatalf("weekly summaries not rendered: %d", rec.Code)
	}
	first := weeks[0][1]
	if !regexp.MustCompile(`datetime="2026-09-02"[^>]*>[^<]*</time>\s*<span class="month-day-total"[^>]*>0/0 h</span>`).MatchString(rec.Body.String()) {
		t.Fatal("vacation day must display zero work and zero bookable capacity")
	}
	position := -1
	for _, label := range []string{"Kapazität:", "Urlaub/Feiertage", "Projekt Alpha", "Projekt Beta", "Gesamt gebucht", "Verfügbar"} {
		next := strings.Index(first, "<dt>"+label+"</dt>")
		if next <= position {
			t.Fatalf("missing/out of order summary row %q", label)
		}
		position = next
	}
	for _, want := range []string{
		"FYW 36", `title="10 h über Wochenkapazität">(+10 h)</span>`,
		`style="--project-color: #123456"`, `style="--project-color: #654321"`,
		"<dt>Projekt Alpha</dt><dd>30 h</dd>", "<dt>Projekt Beta</dt><dd>12 h</dd>",
		"<dt>Kapazität:</dt><dd>32 h</dd>", "<dt>Urlaub/Feiertage</dt><dd>8 h</dd>",
		"<dt>Gesamt gebucht</dt><dd>42 h</dd>", "<dt>Verfügbar</dt><dd>0 h</dd>",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("summary missing %q: %s", want, first)
		}
	}
	if strings.Index(first, "month-over") > strings.Index(first, "<dl") {
		t.Fatal("overload badge must be next to the week label")
	}
	for i, week := range weeks {
		if !regexp.MustCompile(`class="month-summary-row month-project-total" style="--project-color: #32cd32">\s*<dt>Urlaub/Feiertage</dt>`).MatchString(week[1]) {
			t.Fatal("absence summary must use the vacation project's color, including empty weeks")
		}
		if strings.Contains(week[1], plan.Weeks[i].Label) || strings.Contains(week[1], "<dt>Gespeichert</dt>") {
			t.Fatal("obsolete summary date/label still shown")
		}
		if i > 0 && strings.Contains(week[1], "month-over") {
			t.Fatal("empty week must not show an overload badge")
		}
	}
}

func TestFiscalWeekLabels(t *testing.T) {
	h, store := newTestServer(t)
	if err := store.Mutate(func(d *models.Data) error {
		d.Settings.Year = 2027
		d.Settings.FiscalYearStartMonth = 7
		d.Projects = append(d.Projects, models.Project{ID: "p", Name: "Alpha", Active: true, FiscalYear: 2027})
		d.Entries = append(d.Entries, models.Entry{Date: "2026-07-01", ProjectID: "p", Hours: 2})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/goal", "/month?month=2026-07"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "FYW 1") {
			t.Fatalf("%s missing fiscal week identifier", path)
		}
		if regexp.MustCompile(`(?:>|· )W\s*\d`).MatchString(rec.Body.String()) {
			t.Fatalf("%s still uses ambiguous W labels", path)
		}
		if regexp.MustCompile(`FYW\d`).MatchString(rec.Body.String()) {
			t.Fatalf("%s must separate FYW and the number with a space", path)
		}
	}
}
func TestMonthCompactHeadersAndAutomaticEstimates(t *testing.T) {
	_, store := newTestServer(t)
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := models.DefaultData(2026)
	d.Settings.FiscalYearStartMonth = 1
	d.Projects = []models.Project{{ID: "p", Name: "Projekt Alpha", Color: "#123456", FiscalYear: 2026}}
	d.Entries = []models.Entry{
		{Date: "2026-08-31", ProjectID: "p", Hours: 10},
		{Date: "2026-09-22", ProjectID: "p", Hours: 10},
	}
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	plan := forecast.BuildMonthPlan(d, holidays.Get(2026, "SN"), now, now)
	rec := httptest.NewRecorder()
	srv.render(rec, httptest.NewRequest("GET", "/month", nil), "month.html", map[string]any{
		"Settings": d.Settings, "FYYears": []int{2026}, "Plan": plan, "Active": "month",
	})
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("render failed: %s", body)
	}
	header := regexp.MustCompile(`(?s)<div class="month-date">\s*<time datetime="2026-08-31">Montag 31.08.</time>\s*<span class="month-day-total"[^>]*>10/8 h <span class="month-over"[^>]*>\(\+2 h\)</span></span>\s*</div>`)
	if !header.MatchString(body) {
		t.Fatal("weekday, hours and overtime badge must share the date header")
	}
	if !strings.Contains(body, `datetime="2026-09-22" aria-current="date"`) || !strings.Contains(body, `class="month-event estimated"`) {
		t.Fatal("today must be highlighted and estimated without selecting a mode")
	}
	tiles := regexp.MustCompile(`(?s)<div class="month-event[^"]*"[^>]*>(.*?)</div>`).FindAllStringSubmatch(body, -1)
	if len(tiles) == 0 {
		t.Fatal("no project tiles")
	}
	for _, tile := range tiles {
		if !regexp.MustCompile(`^\s*<strong>Projekt Alpha</strong>\s*<span>[0-9.]+ h</span>\s*$`).MatchString(tile[1]) {
			t.Fatalf("tile must contain only project name and hours: %s", tile[1])
		}
	}
}
