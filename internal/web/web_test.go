package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

// newTestHandler builds a server backed by a throwaway data file.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.Handler()
}

func TestSecurityHeadersArePresent(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", csp)
	}
}

func TestCrossSitePostIsRejected(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			name:    "cross-site fetch metadata",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			want:    http.StatusForbidden,
		},
		{
			name:    "foreign origin",
			headers: map[string]string{"Origin": "https://evil.example"},
			want:    http.StatusForbidden,
		},
		{
			name:    "same-origin fetch metadata",
			headers: map[string]string{"Sec-Fetch-Site": "same-origin"},
			want:    http.StatusSeeOther,
		},
		{
			name:    "matching origin",
			headers: map[string]string{"Origin": "http://example.com"},
			want:    http.StatusSeeOther,
		},
		{
			// Non-browser clients send neither header and stay allowed.
			name:    "no browser headers",
			headers: nil,
			want:    http.StatusSeeOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://example.com/private", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestStaticAssetsAreVersionedAndCacheable(t *testing.T) {
	h := newTestHandler(t)

	url := assetURL("/static/style.css")
	if !strings.Contains(url, "?v=") {
		t.Fatalf("assetURL = %q, want a version query", url)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want an immutable directive", cc)
	}
}

// The private mode replaces the real document with sample data instead of
// blanking the figures out, so the page keeps its bars and charts while nothing
// real is on screen.
func TestPrivateModeShowsSampleDataInsteadOfTheRealOne(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "9998887", Name: "Geheimprojekt", BudgetHours: 120,
			Color: "#2563eb", Active: true, FiscalYear: d.Settings.Year,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	for _, path := range []string{"/", "/projects", "/goal", "/week"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusFound {
			t.Fatalf("GET %s = %d, want 200/302", path, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "Geheimprojekt") || strings.Contains(body, "9998887") {
			t.Errorf("GET %s leaks the real project", path)
		}
	}

	// The dashboard has to show real figures again - just fictional ones.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Cloud-Migration") {
		t.Error("private dashboard does not show the sample projects")
	}
	if strings.Contains(rec.Body.String(), `<span style="width:0%"`) {
		t.Error("private dashboard still collapses its bars")
	}
}

// The private mode is read-only: it must not be a way to change or export the
// document it is hiding.
func TestPrivateModeBlocksExportAndYearSwitch(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()
	year := store.Snapshot().Settings.Year

	req := httptest.NewRequest(http.MethodGet, "/export", nil)
	req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("private export = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/fy", strings.NewReader("year="+strconv.Itoa(year+1)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := store.Snapshot().Settings.Year; got != year {
		t.Errorf("private mode switched the fiscal year to %d, want %d", got, year)
	}
}

// The working-time figure has to reach both pages: the dashboard tile shows the
// 6-month balancing period, the goal page every window of the chart.
func TestWorkloadReachesDashboardAndGoal(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	// Twelve hours on every day of the last four weeks: well over the 8 h the
	// law averages out to, so the page has to say so. The next two weeks are
	// planned at a sane pace, which is what the second half of the chart shows.
	now := time.Now().UTC().Truncate(24 * time.Hour)
	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 1000,
			Color: "#2563eb", Active: true, FiscalYear: d.Settings.Year,
		})
		for i := -28; i < 14; i++ {
			day := now.AddDate(0, 0, i)
			if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
				continue
			}
			hours := 12.0
			if i >= 0 {
				hours = 6
			}
			d.Entries = append(d.Entries, models.Entry{
				Date: day.Format("2006-01-02"), ProjectID: "p1", Hours: hours,
			})
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	month := forecast.BuildWorkload(store.Snapshot(), 1)
	if !month.HasData || !month.Over {
		t.Fatalf("seeded month is not over the limit: %+v", month)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "Durchschnitt 6 Monate") {
		t.Error("dashboard has no working-time tile")
	}
	// The tile is split: booked on the left, planned on the right.
	if !strings.Contains(rec.Body.String(), "kpi-split") {
		t.Error("dashboard tile does not show both directions")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/goal", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `id="arbeitszeit"`) {
		t.Fatal("goal page has no working-time section")
	}
	// One timeline carries both directions: the overloaded past in the warning
	// colour, the planned months translucent to the right of the today marker.
	if !strings.Contains(body, `fill="#dc2626"`) {
		t.Error("the overloaded months are not marked in the chart")
	}
	if !strings.Contains(body, `>heute<`) {
		t.Error("the chart has no today marker")
	}
	if !strings.Contains(body, `h geplant auf`) {
		t.Error("the chart carries no forward-looking month")
	}
}

func TestOutOfWindowCellsStayVisibleAndWritable(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	d := store.Snapshot()
	_, fyEnd := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	last := fyEnd.Format("2006-01-02")
	// A window covering only the last FY day leaves week 1 outside of it.
	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 10, Color: "#2563eb",
			Active: true, FiscalYear: d.Settings.Year, StartDate: last, EndDate: last,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	day := forecast.FYWeekMonday(d.Settings.Year, d.Settings.FiscalYearStartMonth, 1).Format("2006-01-02")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/week/1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("week status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="h_p1_`+day+`"`) {
		t.Error("out-of-window day has no input in the forecast grid")
	}

	req := httptest.NewRequest(http.MethodPost, "/week/cells",
		strings.NewReader(`{"cells":[{"date":"`+day+`","projectId":"p1","hours":4}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"skipped":0`) {
		t.Fatalf("out-of-window cell rejected: %d %s", rec.Code, rec.Body.String())
	}
	got := 0.0
	for _, e := range store.Snapshot().Entries {
		if e.Date == day && e.ProjectID == "p1" {
			got = e.Hours
		}
	}
	if got != 4 {
		t.Errorf("persisted hours = %v, want 4", got)
	}
}

func TestJSONEditorIsGone(t *testing.T) {
	h := newTestHandler(t)
	for _, path := range []string{"/data", "/data/reset", "/data/ai"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/goal", nil))
	if strings.Contains(rec.Body.String(), `href="/data"`) {
		t.Error("navigation still links to the removed JSON editor")
	}
}

func TestGoalChatNeedsConfiguredEndpoint(t *testing.T) {
	h := newTestHandler(t)

	// Without an AI endpoint the section reports it instead of calling out.
	req := httptest.NewRequest(http.MethodPost, "/goal/chat",
		strings.NewReader(`{"prompt":"Fasse meine Projekte zusammen"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured chat = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}

	// An empty prompt never reaches the endpoint.
	req = httptest.NewRequest(http.MethodPost, "/goal/chat", strings.NewReader(`{"prompt":"  "}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty prompt = %d, want 400", rec.Code)
	}

	// Private mode keeps the figures from leaving the machine.
	req = httptest.NewRequest(http.MethodPost, "/goal/chat", strings.NewReader(`{"prompt":"Zusammenfassung"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("private chat = %d, want 403", rec.Code)
	}
}

func TestChatContextCarriesTheFigures(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	d := store.Snapshot()
	fyStart, _ := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	day := fyStart.Format("2006-01-02")
	if err := store.Mutate(func(d *models.Data) error {
		noHolidays := 0
		// The FY target is derived: 1440 h gross with nothing deducted.
		d.FiscalYears[d.Settings.Year] = models.FiscalYearSettings{WeekdayHours: 1440, HolidayDays: &noHolidays}
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Color: "#2563eb",
			Active: true, FiscalYear: d.Settings.Year,
		})
		d.Entries = append(d.Entries, models.Entry{Date: day, ProjectID: "p1", Hours: 6})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap := store.Snapshot()
	cal := holidays.Get(snap.Settings.Year, snap.Settings.FederalState)
	got := buildChatContext(snap, cal, forecast.BuildYearSummary(snap, cal), forecast.BuildGoalSummary(snap, cal))
	for _, want := range []string{"Fiskaljahr", "Jahresziel: 1440 h", "Alpha", "6 h"} {
		if !strings.Contains(got, want) {
			t.Errorf("chat context is missing %q:\n%s", want, got)
		}
	}
}

// The active flag has its own route (and its own button) because switching a
// project to inactive keeps every booked hour but releases the budget that was
// never planned. Saving the edit form must not touch the flag.
func TestProjectActiveToggleReleasesBudget(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	h := srv.Handler()

	d := store.Snapshot()
	fyStart, _ := forecast.FiscalYear(d.Settings.Year, d.Settings.FiscalYearStartMonth)
	day := fyStart.Format("2006-01-02")
	if err := store.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Color: "#2563eb",
			Active: true, FiscalYear: d.Settings.Year,
		})
		d.Entries = append(d.Entries, models.Entry{Date: day, ProjectID: "p1", Hours: 10})
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	summary := func() forecast.ProjectSummary {
		t.Helper()
		snap := store.Snapshot()
		ys := forecast.BuildYearSummary(snap, holidays.Get(snap.Settings.Year, snap.Settings.FederalState))
		for _, ps := range ys.Projects {
			if ps.Project.ID == "p1" {
				return ps
			}
		}
		t.Fatal("project p1 disappeared from the summary")
		return forecast.ProjectSummary{}
	}

	if rec := post("/projects/p1/active", "active=0"); rec.Code != http.StatusSeeOther {
		t.Fatalf("deactivate = %d, want 303", rec.Code)
	}
	ps := summary()
	if ps.Project.Active {
		t.Error("project is still active after posting active=0")
	}
	if ps.Released != 90 || ps.AvailableBudget != 10 || ps.Remaining != 0 {
		t.Errorf("released/available/remaining = %v/%v/%v, want 90/10/0", ps.Released, ps.AvailableBudget, ps.Remaining)
	}
	if ps.Consumed != 10 {
		t.Errorf("Consumed = %v, want 10 (booked hours are kept)", ps.Consumed)
	}

	// The edit form no longer carries the flag, so saving it must not revive it.
	if rec := post("/projects/p1/update", "name=Alpha+2&assignmentId=1&budget=100&color=%232563eb"); rec.Code != http.StatusSeeOther {
		t.Fatalf("update = %d, want 303", rec.Code)
	}
	if ps := summary(); ps.Project.Active || ps.Project.Name != "Alpha 2" {
		t.Errorf("after update: active=%v name=%q, want false/\"Alpha 2\"", ps.Project.Active, ps.Project.Name)
	}

	if rec := post("/projects/p1/active", "active=1"); rec.Code != http.StatusSeeOther {
		t.Fatalf("reactivate = %d, want 303", rec.Code)
	}
	if ps := summary(); !ps.Project.Active || ps.Released != 0 || ps.Remaining != 90 {
		t.Errorf("reactivated: active=%v released=%v remaining=%v, want true/0/90", ps.Project.Active, ps.Released, ps.Remaining)
	}
}

// The progress chart shows the hours on the left and the same gridlines as a
// share of the target on the right, with the target line labelled 100 %.
func TestProgressChartPercentAxis(t *testing.T) {
	labels := []string{"Jul", "Aug"}
	booked := []float64{50, 100}
	projected := []float64{50, 150}

	got := string(progressSVG(labels, booked, projected, 200, 1, false))
	for _, want := range []string{">0 %<", ">100 %<"} {
		if !strings.Contains(got, want) {
			t.Errorf("percentage axis is missing %q", want)
		}
	}

	// Without a target a percentage has no basis.
	if got := string(progressSVG(labels, booked, projected, 0, 1, false)); strings.Contains(got, " %<") {
		t.Error("chart without a target must not draw a percentage axis")
	}
}

// Booked hours only exist on past days, so the sub-period today falls into
// already carries its final booked value. The green curve must therefore end on
// the full booked total and not on a value interpolated across that sub-period,
// otherwise a target that is already met still looks unreached.
func TestProgressChartBookedEndsAtTotal(t *testing.T) {
	labels := []string{"Jul", "Aug", "Sep"}
	booked := []float64{250, 360, 360}    // cumulative, nothing booked in Sep yet
	projected := []float64{250, 400, 646} // cumulative incl. forecast
	const target, todayPos = 359, 1.548   // mid-August

	got := string(progressSVG(labels, booked, projected, target, todayPos, false))

	green := regexp.MustCompile(`<polyline fill="none" stroke="#16a34a" stroke-width="2.5" points="([^"]+)"`).FindStringSubmatch(got)
	if green == nil {
		t.Fatal("booked curve is missing")
	}
	pts := strings.Fields(green[1])
	endY, err := strconv.ParseFloat(strings.SplitN(pts[len(pts)-1], ",", 2)[1], 64)
	if err != nil {
		t.Fatalf("parsing the curve end %q: %v", pts[len(pts)-1], err)
	}

	tgt := regexp.MustCompile(`<line x1="[\d.]+" y1="([\d.]+)" x2="[\d.]+" y2="[\d.]+" stroke="#dc2626" stroke-width="2"/>`).FindStringSubmatch(got)
	if tgt == nil {
		t.Fatal("target line is missing")
	}
	targetY, err := strconv.ParseFloat(tgt[1], 64)
	if err != nil {
		t.Fatalf("parsing the target line: %v", err)
	}

	// Smaller y means higher up: 360 h booked must sit above the 359 h target.
	if endY >= targetY {
		t.Errorf("booked curve ends at y=%g, want above the target line at y=%g", endY, targetY)
	}
}
