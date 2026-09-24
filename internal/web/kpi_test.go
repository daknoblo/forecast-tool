package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/sample"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

func TestDashboardKPIHeadingsAndCaptions(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			st, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
			if err != nil {
				t.Fatal(err)
			}
			if populated {
				if err := st.Mutate(func(d *models.Data) error {
					*d = sample.Data(time.Now().UTC(), *d)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			srv, err := NewServer(st, nil)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			row, _, _ := strings.Cut(rec.Body.String(), "</section>")
			if strings.Count(row, `class="card kpi"`) != 8 {
				t.Fatal("dashboard must render all eight KPI tiles")
			}
			for _, heading := range []string{"Ø 6 Monate", "Forecast Accuracy"} {
				marker := `<div class="kpi-heading">` + heading + `</div>`
				if strings.Count(row, marker) != 1 {
					t.Fatalf("expected exactly one heading %q", heading)
				}
				_, tile, _ := strings.Cut(row, marker)
				tile, _, _ = strings.Cut(tile, `<div class="card kpi"`)
				if !strings.HasPrefix(strings.TrimSpace(tile), `<div class="kpi-value kpi-split">`) {
					t.Fatalf("%s heading must directly precede the figures", heading)
				}
				if strings.Count(tile, `class="kpi-part`) != 2 || strings.Count(tile, "<small>") != 2 {
					t.Fatalf("%s must retain two figures and captions, even without data", heading)
				}
				if strings.Contains(row, `<div class="kpi-label">`+heading+`</div>`) {
					t.Fatalf("%s still rendered below figures", heading)
				}
			}
			for _, caption := range []string{"Rückblick", "Forecast", "Aktuell · ESXP", "Minimum FY-Ende"} {
				if !strings.Contains(row, "<small>"+caption+"</small>") {
					t.Fatalf("missing caption: %s", caption)
				}
			}
			if strings.Count(row, `href="/goal#arbeitszeit"`) != 2 {
				t.Fatal("workload links lost")
			}
			if strings.Contains(row, `<div>Stand `) || strings.Contains(row, `<div class="kpi-sub">Stand `) {
				t.Fatal("observation date must only appear in the tooltip")
			}
		})
	}
}
