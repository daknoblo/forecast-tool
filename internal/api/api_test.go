package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

const (
	readTok  = "read-secret-token"
	writeTok = "write-secret-token"
)

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	st, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	return st
}

func newTestServer(t *testing.T, st *storage.Store, read, write string) http.Handler {
	t.Helper()
	t.Setenv(ReadTokenEnv, read)
	t.Setenv(WriteTokenEnv, write)
	return New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func activeYear(t *testing.T, st *storage.Store) int {
	t.Helper()
	return st.Snapshot().Settings.Year
}

func seedProject(t *testing.T, st *storage.Store, p models.Project) {
	t.Helper()
	if err := st.Update(func(d *models.Data) error {
		d.Projects = append(d.Projects, p)
		return nil
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func TestAuthMatrix(t *testing.T) {
	st := newTestStore(t)
	h := newTestServer(t, st, readTok, writeTok)

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		body   any
		want   int
	}{
		{"no token", http.MethodGet, "/api/v1/data", "", nil, http.StatusUnauthorized},
		{"bad token", http.MethodGet, "/api/v1/data", "nope", nil, http.StatusUnauthorized},
		{"read GET", http.MethodGet, "/api/v1/data", readTok, nil, http.StatusOK},
		{"write GET", http.MethodGet, "/api/v1/data", writeTok, nil, http.StatusOK},
		{"read cannot write", http.MethodPost, "/api/v1/projects", readTok, map[string]any{"name": "X"}, http.StatusForbidden},
		{"write can write", http.MethodPost, "/api/v1/projects", writeTok, map[string]any{"name": "X"}, http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := do(t, h, c.method, c.path, c.token, c.body)
			if rr.Code != c.want {
				t.Fatalf("got %d, want %d (body: %s)", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

func TestAPIDisabledWhenNoTokens(t *testing.T) {
	st := newTestStore(t)
	h := newTestServer(t, st, "", "")
	rr := do(t, h, http.MethodGet, "/api/v1/data", readTok, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestReadTokenGrantsOnlyRead(t *testing.T) {
	st := newTestStore(t)
	// Only a write token configured: a read-only client has no valid token.
	h := newTestServer(t, st, "", writeTok)
	if rr := do(t, h, http.MethodGet, "/api/v1/data", readTok, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("stale read token should be 401, got %d", rr.Code)
	}
	if rr := do(t, h, http.MethodGet, "/api/v1/data", writeTok, nil); rr.Code != http.StatusOK {
		t.Fatalf("write token GET should be 200, got %d", rr.Code)
	}
}

func TestSyncEntries(t *testing.T) {
	st := newTestStore(t)
	year := activeYear(t, st)
	seedProject(t, st, models.Project{ID: "p1", Name: "Projekt 1", BudgetHours: 100, Color: "#3cb44b", Active: true, FiscalYear: year})
	seedProject(t, st, models.Project{ID: "pw", Name: "Fenster", BudgetHours: 40, Color: "#4363d8", Active: true, FiscalYear: year, StartDate: fmt.Sprintf("%04d-07-01", year), EndDate: fmt.Sprintf("%04d-07-31", year)})
	h := newTestServer(t, st, readTok, writeTok)

	day := fmt.Sprintf("%04d-07-01", year)
	body := map[string]any{"entries": []map[string]any{
		{"date": day, "projectId": "p1", "hours": 6, "kind": "forecast"},
		{"date": day, "projectId": "p1", "hours": 4, "kind": "actual"},
		// unknown project -> skipped
		{"date": day, "projectId": "ghost", "hours": 3, "kind": "actual"},
		// out of window -> skipped
		{"date": fmt.Sprintf("%04d-09-01", year), "projectId": "pw", "hours": 2, "kind": "forecast"},
	}}
	rr := do(t, h, http.MethodPost, "/api/v1/entries/sync", writeTok, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("sync got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var res syncResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode sync result: %v", err)
	}
	if res.Upserted != 2 || res.Deleted != 0 || len(res.Skipped) != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Verify persisted entries via the list endpoint.
	rr = do(t, h, http.MethodGet, "/api/v1/entries?projectId=p1", readTok, nil)
	var listed struct {
		Entries []models.Entry `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode entries: %v", err)
	}
	if len(listed.Entries) != 2 {
		t.Fatalf("want 2 persisted entries, got %d", len(listed.Entries))
	}

	// hours=0 deletes the forecast entry.
	del := map[string]any{"entries": []map[string]any{{"date": day, "projectId": "p1", "hours": 0, "kind": "forecast"}}}
	rr = do(t, h, http.MethodPost, "/api/v1/entries/sync", writeTok, del)
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode delete result: %v", err)
	}
	if res.Deleted != 1 || res.Upserted != 0 {
		t.Fatalf("delete result unexpected: %+v", res)
	}
	rr = do(t, h, http.MethodGet, "/api/v1/entries?projectId=p1&kind=forecast", readTok, nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &listed)
	if len(listed.Entries) != 0 {
		t.Fatalf("forecast entry should be deleted, got %d", len(listed.Entries))
	}
}

func TestProjectsCRUD(t *testing.T) {
	st := newTestStore(t)
	h := newTestServer(t, st, readTok, writeTok)

	// Create.
	rr := do(t, h, http.MethodPost, "/api/v1/projects", writeTok, map[string]any{"name": "Neu", "budgetHours": 80})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create got %d (body: %s)", rr.Code, rr.Body.String())
	}
	var created models.Project
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Name != "Neu" || created.BudgetHours != 80 {
		t.Fatalf("unexpected created project: %+v", created)
	}

	// Update.
	rr = do(t, h, http.MethodPut, "/api/v1/projects/"+created.ID, writeTok, map[string]any{"budgetHours": 120, "active": false})
	if rr.Code != http.StatusOK {
		t.Fatalf("update got %d (body: %s)", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodGet, "/api/v1/projects/"+created.ID, readTok, nil)
	var got models.Project
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.BudgetHours != 120 || got.Active {
		t.Fatalf("update not reflected: %+v", got)
	}

	// Delete.
	if rr := do(t, h, http.MethodDelete, "/api/v1/projects/"+created.ID, writeTok, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete got %d", rr.Code)
	}
	if rr := do(t, h, http.MethodGet, "/api/v1/projects/"+created.ID, readTok, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("deleted project should be 404, got %d", rr.Code)
	}
}

func TestVacationProjectLocked(t *testing.T) {
	st := newTestStore(t)
	year := activeYear(t, st)
	h := newTestServer(t, st, readTok, writeTok)
	vacID := models.VacationProjectID(year)

	if rr := do(t, h, http.MethodPut, "/api/v1/projects/"+vacID, writeTok, map[string]any{"name": "Hacked"}); rr.Code != http.StatusConflict {
		t.Fatalf("vacation update should be 409, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if rr := do(t, h, http.MethodDelete, "/api/v1/projects/"+vacID, writeTok, nil); rr.Code != http.StatusConflict {
		t.Fatalf("vacation delete should be 409, got %d", rr.Code)
	}
}

func TestSettingsAndFY(t *testing.T) {
	st := newTestStore(t)
	year := activeYear(t, st)
	h := newTestServer(t, st, readTok, writeTok)

	// Global settings update.
	if rr := do(t, h, http.MethodPut, "/api/v1/settings", writeTok, map[string]any{"federalState": "BY", "weeklyTargetHours": 42}); rr.Code != http.StatusOK {
		t.Fatalf("settings update got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := st.Snapshot().Settings; got.FederalState != "BY" || got.WeeklyTargetHours != 42 {
		t.Fatalf("settings not applied: %+v", got)
	}
	// Invalid federal state rejected.
	if rr := do(t, h, http.MethodPut, "/api/v1/settings", writeTok, map[string]any{"federalState": "XX"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid state should be 400, got %d", rr.Code)
	}

	// Per-FY settings drive the vacation project budget: (10+5)*8 = 120h.
	path := fmt.Sprintf("/api/v1/settings/fiscal-years/%d", year)
	if rr := do(t, h, http.MethodPut, path, writeTok, map[string]any{"vacationDaysH1": 10, "vacationDaysH2": 5}); rr.Code != http.StatusOK {
		t.Fatalf("fy settings got %d (body: %s)", rr.Code, rr.Body.String())
	}
	rr := do(t, h, http.MethodGet, "/api/v1/projects/"+models.VacationProjectID(year), readTok, nil)
	var vac models.Project
	_ = json.Unmarshal(rr.Body.Bytes(), &vac)
	if vac.BudgetHours != 120 {
		t.Fatalf("vacation budget should be 120, got %v", vac.BudgetHours)
	}
}

func TestGetDataRedactsAIKey(t *testing.T) {
	st := newTestStore(t)
	if err := st.Update(func(d *models.Data) error {
		d.Settings.AI.APIKey = "super-secret-key"
		return nil
	}); err != nil {
		t.Fatalf("seed ai key: %v", err)
	}
	h := newTestServer(t, st, readTok, writeTok)
	rr := do(t, h, http.MethodGet, "/api/v1/data", readTok, nil)
	if strings.Contains(rr.Body.String(), "super-secret-key") {
		t.Fatalf("GET /data leaked the AI key: %s", rr.Body.String())
	}
}
