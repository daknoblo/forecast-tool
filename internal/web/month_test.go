package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

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
		{"/month?view=wrong", false, 400, "Ungültige Kalenderansicht"},
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
		if tc.status == 200 && (strings.Count(body, `scope="col"`) != 6 || strings.Contains(body, `<th scope="col">Samstag</th>`) || strings.Contains(body, `<th scope="col">Sonntag</th>`)) {
			t.Fatal("calendar must have five weekday columns plus the weekly summary")
		}
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("GET calendar mutated stored data")
	}
}

func TestMonthTemplateHasNoWriteInteraction(t *testing.T) {
	b, err := os.ReadFile("templates/month.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<form", "<script", "fetch(", "data-autosave"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("calendar preview must stay read-only: %s", forbidden)
		}
	}
}
