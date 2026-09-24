package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/api"
	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

func TestDashboardForecastAccuracyImportAndPrivacy(t *testing.T) {
	t.Setenv(api.WriteTokenEnv, "test-write")
	st, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A fixed, finished FY makes the rendered projection independent of today.
	if err := st.Mutate(func(d *models.Data) error { d.Settings.Year = 2020; return nil }); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	tile := func(private bool) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if private {
			req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("dashboard: %d %s", rr.Code, rr.Body.String())
		}
		_, result, ok := strings.Cut(rr.Body.String(), `id="forecast-accuracy"`)
		if !ok {
			t.Fatal("missing tile")
		}
		result, _, _ = strings.Cut(result, `<div class="card kpi"`)
		return result
	}
	if got := tile(false); !strings.Contains(got, "Noch kein ESXP-Wert") || strings.Contains(got, "<span class=\"kpi-part\">0 %") {
		t.Fatalf("missing value fabricated: %s", got)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/forecast-accuracy/2020", strings.NewReader(`{"percentage":87.65,"asOf":"2020-07-01"}`))
	req.Header.Set("Authorization", "Bearer test-write")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	got := tile(false)
	percent := formatHours(87.65) + " %"
	if !strings.Contains(got, percent) || !strings.Contains(got, "Stand 01.07.2020") ||
		!strings.Contains(got, "Minimum FY-Ende") || strings.Contains(got, "Stand veraltet") {
		t.Fatalf("missing imported values: %s", got)
	}
	if got := tile(true); strings.Contains(got, percent) || !strings.Contains(got, "90 %") {
		t.Fatalf("private accuracy leaked or not replaced: %s", got)
	}
	if err := st.Mutate(func(d *models.Data) error {
		d.Settings.FiscalYearStartMonth = 8
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := tile(false); !strings.Contains(got, "FY-Startmonat wurde geändert") {
		t.Fatal(got)
	}
	// A later read cannot silently reinterpret an older snapshot as current.
	now := time.Now().UTC()
	year := forecast.FiscalYearOf(now, 7) - 1
	if err := st.Mutate(func(d *models.Data) error {
		d.Settings.Year, d.Settings.FiscalYearStartMonth = year, 7
		d.ForecastAccuracy[year] = models.ForecastAccuracy{
			Percentage: 100, AsOf: fmt.Sprintf("%d-09-24", year-1), FiscalYearStartMonth: 7,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := tile(false); !strings.Contains(got, "Stand veraltet") {
		t.Fatal(got)
	}
}
