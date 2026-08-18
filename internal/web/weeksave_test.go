package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// The week form posts one field per project and day. A field naming a project
// that no longer exists (a stale tab, a crafted post) must not create an entry:
// the document would then fail validation and every later auto-save would break.
func TestWeekSaveIgnoresUnknownProjects(t *testing.T) {
	h, store := newTestServer(t)
	d := store.Snapshot()
	year, sm := d.Settings.Year, d.Settings.FiscalYearStartMonth

	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100,
			Color: "#2563eb", Active: true, FiscalYear: year,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	monday := forecast.FYWeekMonday(year, sm, 1)
	day := monday.Format("2006-01-02")
	form := url.Values{
		"h_p1_" + day:    {"6"},
		"h_ghost_" + day: {"4"}, // project was deleted meanwhile
	}
	req := httptest.NewRequest(http.MethodPost, "/week/1?weeks=1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("week save = %d, want 303", rec.Code)
	}

	for _, e := range store.Snapshot().Entries {
		if e.ProjectID == "ghost" {
			t.Errorf("an entry for a non-existent project was stored: %+v", e)
		}
	}
	// The document has to stay writable through the validating path.
	if err := store.Mutate(func(d *models.Data) error { return nil }); err != nil {
		t.Errorf("the document no longer validates after the week save: %v", err)
	}
	// The legitimate cell was saved.
	var got float64
	for _, e := range store.Snapshot().Entries {
		if e.ProjectID == "p1" && e.Date == day {
			got = e.Hours
		}
	}
	if got != 6 {
		t.Errorf("p1 on %s = %v h, want 6", day, got)
	}
}

// The auto-save endpoint already skips unknown projects; it must report how
// many it dropped instead of failing the whole batch.
func TestWeekCellsReportSkippedProjects(t *testing.T) {
	h, store := newTestServer(t)
	d := store.Snapshot()
	year, sm := d.Settings.Year, d.Settings.FiscalYearStartMonth
	day := forecast.FYWeekMonday(year, sm, 1).Format("2006-01-02")

	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100,
			Color: "#2563eb", Active: true, FiscalYear: year,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := `{"cells":[{"date":"` + day + `","projectId":"p1","hours":3},` +
		`{"date":"` + day + `","projectId":"ghost","hours":9}]}`
	req := httptest.NewRequest(http.MethodPost, "/week/cells", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"skipped":1`) {
		t.Errorf("response = %s, want skipped:1", rec.Body.String())
	}
	for _, e := range store.Snapshot().Entries {
		if e.ProjectID == "ghost" {
			t.Error("the unknown project was stored anyway")
		}
	}
}

// The first fiscal-year week can start in the previous fiscal year. Those cells
// stay visible and writable, but they must be marked so the live recalculation
// keeps them out of this year's sums - exactly like the server does.
func TestWeekGridMarksOutOfYearCells(t *testing.T) {
	h, store := newTestServer(t)
	d := store.Snapshot()
	year, sm := d.Settings.Year, d.Settings.FiscalYearStartMonth
	fyStart, _ := forecast.FiscalYear(year, sm)
	monday := forecast.FYWeekMonday(year, sm, 1)
	if !monday.Before(fyStart) {
		t.Skip("the first FY week starts on the fiscal-year border this time")
	}

	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100,
			Color: "#2563eb", Active: true, FiscalYear: year,
		})
		d.Entries = append(d.Entries,
			models.Entry{Date: monday.Format("2006-01-02"), ProjectID: "p1", Hours: 8},
			models.Entry{Date: fyStart.Format("2006-01-02"), ProjectID: "p1", Hours: 5},
		)
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/week/1?weeks=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-date="`+monday.Format("2006-01-02")+`" data-outyear="1"`) {
		t.Error("the out-of-year cell is not marked for the client-side recalculation")
	}
	if strings.Contains(body, `data-date="`+fyStart.Format("2006-01-02")+`" data-outyear="1"`) {
		t.Error("a day inside the fiscal year was marked as out-of-year")
	}

	// The grid and the dashboard must agree on the week.
	snap := store.Snapshot()
	cal := holidays.Get(year, snap.Settings.FederalState)
	wv := forecast.BuildWeek(snap, cal, 1)
	ys := forecast.BuildYearSummary(snap, cal)
	if wv.Total != 5 {
		t.Errorf("grid week 1 = %v h, want 5 (the 8 h belong to the previous FY)", wv.Total)
	}
	if ys.WeekTotals[0].Hours != wv.Total {
		t.Errorf("dashboard week 1 = %v h, grid = %v h", ys.WeekTotals[0].Hours, wv.Total)
	}
}
