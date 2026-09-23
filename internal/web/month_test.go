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
		{"/month?month=2026-07", false, 200, "Nur Vorschau"},
		{"/month?month=2026-07", true, 200, "frei erfundene Beispieldaten"},
		{"/month?month=2020-01", false, 200, "Juli 2026"},
		{"/month?month=2026-13", false, 400, "Ungültiger Monat"},
		{"/month?view=wrong", false, 200, "Ab heute: Schätzung"},
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
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("GET calendar mutated stored data")
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
