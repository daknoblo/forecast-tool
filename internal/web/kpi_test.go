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

func TestDashboardBudgetCoverage(t *testing.T) {
	for _, language := range []string{"de", "en"} {
		for _, tc := range []struct {
			name                 string
			budget, carry, gross float64
			closed               bool
			wantHours, wantPct   string
		}{
			{"below-target", 50, 0, 108, false, "50 h", "50 %"},
			{"at-target", 100, 0, 108, false, "100 h", "100 %"},
			{"above-target", 1776, 0, 108, false, "1776 h", "1776 %"},
			{"rounding", 50, 0, 308, false, "50 h", "16.7 %"},
			{"carry-over", 100, 20, 108, false, "80 h", "80 %"},
			{"released", 100, 20, 108, true, "8 h", "8 %"},
			{"no-budget", 0, 0, 108, false, "0 h", "0 %"},
			{"no-target", 100, 0, 8, false, "100 h", "–"},
			{"negative-target", 100, 0, 4, false, "100 h", "–"},
		} {
			t.Run(language+"/"+tc.name, func(t *testing.T) {
				st, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Mutate(func(d *models.Data) error {
					*d = models.DefaultData(2027)
					d.Settings.Language = language
					d.Settings.FiscalYearStartMonth = 7
					zero := 0
					d.FiscalYears[2027] = models.FiscalYearSettings{
						WeekdayHours: tc.gross, VacationDays: 1, HolidayDays: &zero,
					}
					d.Projects = []models.Project{
						{ID: "p", AssignmentID: "1", Name: "Project", FiscalYear: 2027, BudgetHours: tc.budget, Active: !tc.closed, Color: "#2563eb"},
						{ID: "old", AssignmentID: "1", Name: "Previous", FiscalYear: 2026, BudgetHours: tc.budget, Active: true, Color: "#2563eb"},
					}
					models.EnsureVacationProject(d, 2027)
					if tc.carry > 0 {
						d.Entries = append(d.Entries, models.Entry{Date: "2025-11-03", ProjectID: "old", Hours: tc.carry})
					}
					if tc.budget > 0 {
						d.Entries = append(d.Entries, models.Entry{Date: "2026-11-03", ProjectID: "p", Hours: 8})
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
				_, tile, found := strings.Cut(rec.Body.String(), `class="kpi-value kpi-split" id="budget-coverage">`)
				if !found {
					t.Fatal("missing split budget tile")
				}
				tile, _, _ = strings.Cut(tile, `<div class="card kpi"`)
				parts := strings.Split(tile, `<span class="kpi-part">`)
				if len(parts) != 3 {
					t.Fatal("budget tile must retain two figures")
				}
				for i, want := range []string{tc.wantHours, tc.wantPct} {
					got, _, _ := strings.Cut(parts[i+1], "</span>")
					if got != want {
						t.Errorf("figure %d = %q, want %q", i, got, want)
					}
				}
				caption := "FY-Abdeckung"
				if language == "en" {
					caption = "FY coverage"
				}
				if !strings.Contains(tile, "<small>"+caption+"</small>") {
					t.Fatalf("missing caption %q", caption)
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
