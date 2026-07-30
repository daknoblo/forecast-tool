package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/forecast"
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

func TestPrivateModeMasksFigures(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), maskedValue) {
		t.Error("private dashboard does not contain any masked value")
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
