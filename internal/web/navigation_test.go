package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestRetiredWeekRoutesDoNotMutateData(t *testing.T) {
	h, store := newTestServer(t)
	before := store.Snapshot()
	start, _ := forecast.FiscalYear(before.Settings.Year, before.Settings.FiscalYearStartMonth)
	project := models.VacationProjectID(before.Settings.Year)
	date := start.Format("2006-01-02")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, path := range []string{"/week", "/week/1", "/week/1?weeks=4", "/week/cells"} {
			t.Run(method+" "+path, func(t *testing.T) {
				body := fmt.Sprintf("h_%s_%s=8", project, date)
				contentType := "application/x-www-form-urlencoded"
				if path == "/week/cells" {
					body = fmt.Sprintf(`{"cells":[{"date":%q,"projectId":%q,"hours":8}]}`, date, project)
					contentType = "application/json"
				}
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Content-Type", contentType)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("status=%d, want 404: %s", rec.Code, rec.Body.String())
				}
				if !reflect.DeepEqual(store.Snapshot(), before) {
					t.Fatal("retired endpoint changed stored data")
				}
			})
		}
	}
}

func TestNavigationUsesMonthlyPlanningOnly(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{"/", "/projects", "/month", "/goal", "/settings"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		for _, retired := range []string{`href="/week`, `action="/week`, `>Forecast</a>`, "Forecast-Raster"} {
			if strings.Contains(body, retired) {
				t.Errorf("%s still contains %q", path, retired)
			}
		}
		if !strings.Contains(body, `href="/month"`) || !strings.Contains(body, `>Monatsplanung</a>`) {
			t.Errorf("%s lacks monthly navigation", path)
		}
	}
}

func TestEveryFiscalWeekLinksToAMonthlyAnchor(t *testing.T) {
	for _, year := range []int{2027, 2030} {
		t.Run(fmt.Sprint(year), func(t *testing.T) {
			h, store := newTestServer(t)
			if err := store.Mutate(func(d *models.Data) error {
				d.Settings.Year = year
				d.Settings.FiscalYearStartMonth = 7
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			summary := forecast.BuildYearSummary(store.Snapshot(), holidays.Get(year, "BY"))
			pages := map[string]string{}
			for _, week := range summary.WeekTotals {
				body, ok := pages[week.Month]
				if !ok {
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/month?month="+week.Month, nil))
					if rec.Code != http.StatusOK {
						t.Fatalf("month %s: %d", week.Month, rec.Code)
					}
					body = rec.Body.String()
					pages[week.Month] = body
				}
				anchor := fmt.Sprintf(`id="fyw-%d"`, week.Week)
				if strings.Count(body, anchor) != 1 {
					t.Errorf("FYW %d links to %s without a unique %s", week.Week, week.Month, anchor)
				}
			}
		})
	}
}
