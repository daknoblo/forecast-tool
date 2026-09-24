package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/api"
	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/sample"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

func TestBrowserRegression(t *testing.T) {
	if os.Getenv("FORECAST_BROWSER_TESTS") != "1" {
		t.Skip("set FORECAST_BROWSER_TESTS=1 with Node and Playwright Chromium installed")
	}
	t.Setenv(api.ReadTokenEnv, "")
	t.Setenv(api.WriteTokenEnv, "browser-fixture-write")
	for _, scenario := range []string{"dashboard-empty", "dashboard-populated", "month", "month-unallocated"} {
		t.Run(scenario, func(t *testing.T) {
			var handler http.Handler
			var month string
			var verify func()
			if scenario == "month" || scenario == "month-unallocated" {
				f := newMonthAIFixture(t)
				if err := f.store.Mutate(func(d *models.Data) error {
					for i := range d.Projects {
						if d.Projects[i].IsVacation() {
							d.Projects[i].Color = "#32cd32"
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				before := f.store.Snapshot()
				f.plan.Explanation = "- **Projekt Pattern:** Vier Tage mit jeweils zwei Stunden.\n- **Urlaub:** Unverändert am Freitag."
				if scenario == "month-unallocated" {
					f.plan.Entries = f.plan.Entries[:3]
					f.plan.Unallocated = []forecast.MonthAIUnallocated{{
						WeekStart: f.plan.Entries[0].Date, ProjectID: "p", Hours: 2,
						Reason: "Kein passender Block im Projektzeitraum.",
					}}
				}
				answer, err := json.Marshal(f.plan)
				if err != nil {
					t.Fatal(err)
				}
				f.endpoint(t, func(*http.Request) string { return string(answer) })
				handler, month = f.server.Handler(), f.month
				verify = func() {
					after := f.store.Snapshot()
					if scenario == "month-unallocated" {
						if !reflect.DeepEqual(before.Entries, after.Entries) {
							t.Error("blocked browser preview modified entries")
						}
						return
					}
					expected := append(append([]models.Entry{}, before.Entries[1:]...), f.plan.Entries...)
					if len(after.Entries) != len(expected) || after.SavedMonthPlans[month] == "" {
						t.Error("browser save did not persist the exact plan")
					}
					for _, entry := range expected {
						found := false
						for _, actual := range after.Entries {
							found = found || actual == entry
						}
						if !found {
							t.Errorf("browser save lost or changed entry %+v", entry)
						}
					}
					reopened, err := storage.New(f.store.Path())
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(after, reopened.Snapshot()) {
						t.Error("browser changes did not survive reopening storage")
					}
				}
			} else {
				h, store := newTestServer(t)
				handler = h
				if err := store.Mutate(func(d *models.Data) error {
					if scenario == "dashboard-populated" {
						*d = sample.Data(time.Now().UTC(), *d)
					}
					d.ForecastAccuracy = nil
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			script, err := filepath.Abs("../../tools/screenshots/browser.test.mjs")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "--test", script)
			cmd.WaitDelay = 5 * time.Second
			cmd.Env = append(os.Environ(),
				"FORECAST_BROWSER_URL="+server.URL,
				"FORECAST_BROWSER_SCENARIO="+scenario,
				"FORECAST_BROWSER_MONTH="+month,
			)
			output, err := cmd.CombinedOutput()
			t.Log(string(output))
			if err != nil {
				t.Fatalf("browser regression: %v", err)
			}
			if verify != nil {
				verify()
			}
		})
	}
}
