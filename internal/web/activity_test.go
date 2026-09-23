package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSharedActivityIndicator(t *testing.T) {
	f := newMonthAIFixture(t)
	for _, route := range []string{"/", "/projects", "/goal", "/settings", "/week/1", "/month?month=" + f.month} {
		rec := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		body := rec.Body.String()
		if rec.Code != 200 || strings.Count(body, `id="activity"`) != 1 {
			t.Fatalf("%s missing shared indicator: %d", route, rec.Code)
		}
		if strings.Index(body, `id="activity"`) > strings.Index(body, `class="privswitch"`) {
			t.Fatalf("%s: indicator must precede private switch", route)
		}
		if !strings.Contains(body, `id="activity" class="activity" title="Keine laufenden Datenübertragungen" aria-busy="false"`) ||
			strings.Contains(body, `id="month-loading"`) {
			t.Fatalf("%s: incorrect initial activity state", route)
		}
	}
	for _, file := range []string{"partials.html", "week.html", "goal.html", "month.html"} {
		b, err := os.ReadFile("templates/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "window.beginActivity()") ||
			(!strings.Contains(string(b), ".finally(endActivity)") && !strings.Contains(string(b), "endActivity();")) {
			t.Fatalf("%s: request lifecycle not linked to shared activity", file)
		}
	}
}
