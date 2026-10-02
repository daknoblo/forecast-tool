package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/sample"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

func TestDashboardTargetProgress(t *testing.T) {
	now := time.Now().UTC()
	startMonth := int(now.Month())%12 + 1
	currentFY := forecast.FiscalYearOf(now, startMonth)
	for _, language := range []string{"de", "en"} {
		for _, tc := range []struct {
			name       string
			booked     float64
			yearOffset int
			noTarget   bool
			want       string
		}{
			{name: "booked-only", booked: 2, want: "25 %"},
			{name: "over-target", booked: 10, want: "125 %"},
			{name: "no-bookings", want: "0 %"},
			{name: "no-target", noTarget: true, want: "–"},
			{name: "past-fy", booked: 2, yearOffset: -1, want: "25 %"},
			{name: "future-fy", yearOffset: 1, want: "0 %"},
		} {
			t.Run(language+"/"+tc.name, func(t *testing.T) {
				st, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Mutate(func(d *models.Data) error {
					year := currentFY + tc.yearOffset
					*d = models.DefaultData(year)
					d.Settings.Language = language
					d.Settings.FiscalYearStartMonth = startMonth
					zero := 0
					gross := 16.0
					if tc.noTarget {
						gross = 8
					}
					d.FiscalYears[year] = models.FiscalYearSettings{
						WeekdayHours: gross, VacationDays: 1, HolidayDays: &zero,
					}
					d.Projects = []models.Project{{
						ID: "p", AssignmentID: "1", Name: "Project", FiscalYear: year,
						BudgetHours: 100, Active: true, Color: "#2563eb",
					}}
					models.EnsureVacationProject(d, year)
					day := now.AddDate(0, 0, -1)
					if tc.yearOffset != 0 {
						day, _ = forecast.FiscalYear(year, startMonth)
					}
					d.Entries = []models.Entry{
						{Date: day.Format("2006-01-02"), ProjectID: models.VacationProjectID(year), Hours: 8},
					}
					if tc.booked > 0 {
						d.Entries = append(d.Entries, models.Entry{Date: day.Format("2006-01-02"), ProjectID: "p", Hours: tc.booked})
					}
					if tc.yearOffset == 0 {
						d.Entries = append(d.Entries, models.Entry{Date: now.Format("2006-01-02"), ProjectID: "p", Hours: 4})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
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
				_, tile, found := strings.Cut(rec.Body.String(), `id="target-progress">`)
				if !found {
					t.Fatal("missing target progress tile")
				}
				tile, _, _ = strings.Cut(tile, `<div class="card kpi"`)
				if !strings.Contains(tile, `class="kpi-value kpi-split"`) || strings.Count(tile, `class="kpi-part"`) != 2 {
					t.Fatal("target progress must have two split figures")
				}
				parts := strings.Split(tile, `<span class="kpi-part">`)
				actual, _, _ := strings.Cut(parts[2], "</span>")
				if actual != tc.want {
					t.Fatalf("FY target = %q, want %q", actual, tc.want)
				}
				wtd := forecast.BuildWeekToDate(st.Snapshot(), srv.calendar(st.Snapshot()))
				wantWTD := "–"
				if wtd.HasData {
					wantWTD = formatHours(wtd.RatePct) + " %"
				}
				actualWTD, _, _ := strings.Cut(parts[1], "</span>")
				if actualWTD != wantWTD {
					t.Fatalf("Week-to-date changed: got %q, want %q", actualWTD, wantWTD)
				}
				caption := "FY-Ziel"
				if language == "en" {
					caption = "FY target"
				}
				for _, label := range []string{"Week-to-date", caption} {
					if !strings.Contains(tile, "<small>"+label+"</small>") {
						t.Fatalf("missing caption %q", label)
					}
				}
			})
		}
	}
}

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
