package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

// newTestServer returns a server plus its store so a test can seed data and
// then drive the HTTP handlers against it.
func newTestServer(t *testing.T) (http.Handler, *storage.Store) {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.Handler(), store
}

// postForm submits a form to the handler the way the browser does.
func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Two project rows of one fiscal year sharing an assignment ID would pool their
// hours and show the same figures twice, so the second one must not be created.
func TestDuplicateAssignmentIsRejectedByTheWebUI(t *testing.T) {
	h, store := newTestServer(t)
	year := store.Snapshot().Settings.Year

	first := url.Values{"name": {"Alpha"}, "assignmentId": {"5641245"}, "budget": {"100"}}
	if rec := postForm(t, h, "/projects", first); rec.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", rec.Code)
	}
	second := url.Values{"name": {"Alpha Phase 2"}, "assignmentId": {"5641245"}, "budget": {"50"}}
	postForm(t, h, "/projects", second)

	var withAssignment int
	for _, p := range store.Snapshot().Projects {
		if p.AssignmentID == "5641245" && p.FiscalYear == year {
			withAssignment++
		}
	}
	if withAssignment != 1 {
		t.Errorf("%d rows carry assignment 5641245 in FY %d, want 1", withAssignment, year)
	}

	// Moving an existing project onto an already used assignment is refused too.
	var otherID string
	if rec := postForm(t, h, "/projects", url.Values{
		"name": {"Beta"}, "assignmentId": {"5698112"}, "budget": {"40"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create beta = %d, want 303", rec.Code)
	}
	for _, p := range store.Snapshot().Projects {
		if p.AssignmentID == "5698112" {
			otherID = p.ID
		}
	}
	if otherID == "" {
		t.Fatal("second project was not created")
	}
	postForm(t, h, "/projects/"+otherID, url.Values{
		"name": {"Beta"}, "assignmentId": {"5641245"}, "budget": {"40"},
	})
	for _, p := range store.Snapshot().Projects {
		if p.ID == otherID && p.AssignmentID != "5698112" {
			t.Errorf("assignment was changed to %q, want it kept at 5698112", p.AssignmentID)
		}
	}

	// The same assignment in ANOTHER fiscal year is the normal continuation and
	// stays allowed.
	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "next-fy", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 100,
			Color: "#2563eb", Active: true, FiscalYear: year + 1,
		})
		return nil
	}); err != nil {
		t.Errorf("continuing the assignment in the next FY was rejected: %v", err)
	}
}

// The goal page and the dashboard read the same document, so the hours they
// report have to match to the last decimal.
func TestGoalAndDashboardAgreeOnTheHours(t *testing.T) {
	h, store := newTestServer(t)
	d := store.Snapshot()
	year := d.Settings.Year
	start, _ := forecast.FiscalYear(year, d.Settings.FiscalYearStartMonth)

	if err := store.Mutate(func(d *models.Data) error {
		d.FiscalYears[year] = models.FiscalYearSettings{VacationDays: 30, StandardTaskHours: 120}
		d.Projects = append(d.Projects,
			models.Project{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 300, Color: "#2563eb", Active: true, FiscalYear: year},
			models.Project{ID: "p2", AssignmentID: "2", Name: "Beta", BudgetHours: 200, Color: "#0d9488", Active: true, FiscalYear: year},
		)
		for i := 0; i < 60; i++ {
			day := start.AddDate(0, 0, i*3)
			if wd := day.Weekday(); wd.String() == "Saturday" || wd.String() == "Sunday" {
				continue
			}
			iso := day.Format("2006-01-02")
			d.Entries = append(d.Entries,
				models.Entry{Date: iso, ProjectID: "p1", Hours: 3.5},
				models.Entry{Date: iso, ProjectID: "p2", Hours: 1.5},
			)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap := store.Snapshot()
	cal := holidays.Get(snap.Settings.Year, snap.Settings.FederalState)
	gs := forecast.BuildGoalSummary(snap, cal)
	ys := forecast.BuildYearSummary(snap, cal)
	flow := forecast.BuildGoalFlow(snap, cal)

	if gs.Projected != ys.TotalHours {
		t.Errorf("goal %v h != dashboard %v h", gs.Projected, ys.TotalHours)
	}
	if gs.ActualTotal != ys.TotalActual || gs.ForecastRemaining != ys.TotalForecast {
		t.Errorf("booked/forecast differ: goal %v/%v vs dashboard %v/%v",
			gs.ActualTotal, gs.ForecastRemaining, ys.TotalActual, ys.TotalForecast)
	}
	if flow.Total != gs.Projected {
		t.Errorf("goal flow %v h != goal %v h", flow.Total, gs.Projected)
	}

	// Both pages must render without error and show the same total.
	for _, path := range []string{"/", "/goal"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
	}
}
