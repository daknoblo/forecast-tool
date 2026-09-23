package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/foundry"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

type monthAIFixture struct {
	server *Server
	store  *storage.Store
	month  string
	plan   forecast.MonthAIPlan
}

func newMonthAIFixture(t *testing.T) monthAIFixture {
	t.Helper()
	server, store, _ := foundryTestServer(t)
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	cal := holidays.Get(month.Year(), "SN")
	monday := month
	for monday.Weekday() != time.Monday {
		monday = monday.AddDate(0, 0, 1)
	}
	for {
		clear := true
		for i := 0; i < 5; i++ {
			clear = clear && cal.Name(monday.AddDate(0, 0, i).Format("2006-01-02")) == ""
		}
		if clear {
			break
		}
		monday = monday.AddDate(0, 0, 7)
	}
	plan := forecast.MonthAIPlan{Unallocated: []forecast.MonthAIUnallocated{}}
	for i := 0; i < 4; i++ {
		plan.Entries = append(plan.Entries, models.Entry{
			Date: monday.AddDate(0, 0, i).Format("2006-01-02"), ProjectID: "p", Hours: 2,
		})
	}
	plan.Explanation = "Bisher vier Tage mit je zwei Stunden."
	if err := store.Mutate(func(d *models.Data) error {
		d.Settings.Year = month.Year()
		d.Settings.FiscalYearStartMonth = 1
		d.Settings.FederalState = "SN"
		d.Settings.AI.Deployment = "analyst"
		d.Projects = []models.Project{
			{ID: "p", AssignmentID: "assignment-p", Name: "Projekt Pattern", Active: true, FiscalYear: month.Year(), Color: "#123456"},
			{ID: "vac", Name: "Urlaub", System: models.VacationSystem, FiscalYear: month.Year()},
		}
		d.Entries = []models.Entry{
			{Date: monday.Format("2006-01-02"), ProjectID: "p", Hours: 8},
			{Date: monday.AddDate(0, 0, 4).Format("2006-01-02"), ProjectID: "vac", Hours: 4},
			{Date: now.AddDate(0, 0, -7).Format("2006-01-02"), ProjectID: "p", Hours: 2},
			{Date: month.AddDate(0, 1, 1).Format("2006-01-02"), ProjectID: "p", Hours: 3},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return monthAIFixture{server: server, store: store, month: month.Format("2006-01"), plan: plan}
}

func (f monthAIFixture) endpoint(t *testing.T, content func(*http.Request) string) {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content(r)}}},
		}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(endpoint.Close)
	f.server.foundry.source = &mockFoundry{snapshot: foundry.Snapshot{
		Endpoint: endpoint.URL + "/openai/v1", RefreshedAt: time.Now(),
		Deployments: []foundry.Deployment{{Name: "analyst", ModelName: "gpt-5", ModelFormat: "OpenAI", ProvisioningState: "Succeeded"}},
	}}
}

func monthRequest(h http.Handler, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (f monthAIFixture) generate(t *testing.T) (string, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"month": f.month, "prompt": "Bevorzuge vier Tage mit je zwei Stunden."})
	if err != nil {
		t.Fatal(err)
	}
	rec := monthRequest(f.server.Handler(), "/month/generate", "application/json", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		PreviewURL string `json:"previewURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(response.PreviewURL)
	if err != nil {
		t.Fatal(err)
	}
	token := target.Query().Get("preview")
	if token == "" {
		t.Fatal("missing preview token")
	}
	return response.PreviewURL, token
}

func TestMonthAIPreviewThenExplicitSave(t *testing.T) {
	f := newMonthAIFixture(t)
	before := f.store.Snapshot()
	f.endpoint(t, func(r *http.Request) string {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return "{}"
		}
		for _, want := range []string{"Bevorzuge vier Tage", "assignment-p", before.Entries[2].Date, "Wochen"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("AI request missing planning context %q", want)
			}
		}
		if strings.Contains(string(body), "APIKey") || strings.Contains(string(body), "ClientSecret") {
			t.Error("AI context contains credential fields")
		}
		answer, _ := json.Marshal(f.plan)
		return string(answer)
	})
	location, token := f.generate(t)
	if !reflect.DeepEqual(before.Entries, f.store.Snapshot().Entries) {
		t.Fatal("generation modified bookings")
	}
	rec := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", location, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "KI-Vorschau prüfen") || !strings.Contains(rec.Body.String(), f.plan.Explanation) {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Count(rec.Body.String(), `class="month-event estimated"`) != 4 {
		t.Fatal("preview did not render four two-hour blocks")
	}
	rec = monthRequest(f.server.Handler(), "/month/save", "application/x-www-form-urlencoded", "preview="+token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	after := f.store.Snapshot()
	expected := append(append([]models.Entry{}, before.Entries[1:]...), f.plan.Entries...)
	for _, fixed := range expected {
		found := false
		for _, entry := range after.Entries {
			found = found || entry == fixed
		}
		if !found {
			t.Fatalf("save changed immutable entry %+v", fixed)
		}
	}
	reopened, err := storage.New(f.store.Path())
	if err != nil || !reflect.DeepEqual(after, reopened.Snapshot()) {
		t.Fatal("saved forecast did not survive reopening")
	}
	rec = httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/month?month="+f.month, nil))
	if !strings.Contains(rec.Body.String(), "Gespeicherte Tagesverteilung") || strings.Contains(rec.Body.String(), `class="month-event estimated"`) {
		t.Fatal("saved calendar was re-estimated")
	}
	rec = monthRequest(f.server.Handler(), "/month/save", "application/x-www-form-urlencoded", "preview="+token)
	if rec.Code != http.StatusConflict {
		t.Fatal("saved preview was not consumed")
	}
}

func TestMonthAIUnallocatedPreviewCannotBeSaved(t *testing.T) {
	f := newMonthAIFixture(t)
	before := f.store.Snapshot().Entries
	f.plan.Entries = f.plan.Entries[:3]
	f.plan.Unallocated = []forecast.MonthAIUnallocated{{
		WeekStart: f.plan.Entries[0].Date, ProjectID: "p", Hours: 2, Reason: "Kein passender Block.",
	}}
	f.endpoint(t, func(*http.Request) string { answer, _ := json.Marshal(f.plan); return string(answer) })
	location, token := f.generate(t)
	rec := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", location, nil))
	for _, text := range []string{`class="btn primary" disabled>Forecast speichern`, "Kein passender Block.", "2 h nicht verteilbar"} {
		if !strings.Contains(rec.Body.String(), text) {
			t.Errorf("unallocated preview missing %q", text)
		}
		if !strings.Contains(rec.Body.String(), "<dt>Projekt Pattern</dt><dd>8 h</dd>") {
			t.Fatal("preview project summary must include allocated and unallocated hours")
		}
		if !strings.Contains(rec.Body.String(), "<dt>Kapazität:</dt><dd>36 h</dd>") ||
			!strings.Contains(rec.Body.String(), "<dt>Urlaub/Feiertage</dt><dd>4 h</dd>") ||
			!strings.Contains(rec.Body.String(), "<dt>Gesamt gebucht</dt><dd>8 h</dd>") {
			t.Fatal("AI preview must use net capacity and exclude vacation from booked hours")
		}
	}
	rec = monthRequest(f.server.Handler(), "/month/save", "application/x-www-form-urlencoded", "preview="+token)
	if rec.Code != http.StatusConflict || !reflect.DeepEqual(before, f.store.Snapshot().Entries) {
		t.Fatal("incomplete plan saved")
	}
}

func TestMonthAIOverloadSavesWithoutAdditionalConfirmation(t *testing.T) {
	f := newMonthAIFixture(t)
	if err := f.store.Mutate(func(d *models.Data) error {
		d.Entries[0].Hours = 12
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.plan.Entries = []models.Entry{{Date: f.plan.Entries[0].Date, ProjectID: "p", Hours: 12}}
	f.endpoint(t, func(*http.Request) string { answer, _ := json.Marshal(f.plan); return string(answer) })
	location, token := f.generate(t)
	rec := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", location, nil))
	for _, forbidden := range []string{"Auslastung bitte prüfen", `type="checkbox"`, "Auslastungsorientierung:", "confirmOverload"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("preview still includes %q", forbidden)
		}
	}
	if !strings.Contains(rec.Body.String(), "12/8 h") || !strings.Contains(rec.Body.String(), "(+4 h)") {
		t.Fatal("daily overload indicator missing")
	}
	rec = monthRequest(f.server.Handler(), "/month/save", "application/x-www-form-urlencoded", "preview="+token)
	if rec.Code != http.StatusNoContent || f.store.Snapshot().SavedMonthPlans[f.month] == "" {
		t.Fatalf("overload not saved: %d %s", rec.Code, rec.Body.String())
	}
	reopened, err := storage.New(f.store.Path())
	if err != nil || !reflect.DeepEqual(f.store.Snapshot(), reopened.Snapshot()) {
		t.Fatal("forecast did not persist")
	}
}

func TestMonthAIConcurrentGenerationAndSourceChange(t *testing.T) {
	f := newMonthAIFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.endpoint(t, func(*http.Request) string {
		close(entered)
		<-release
		answer, _ := json.Marshal(f.plan)
		return string(answer)
	})
	body := `{"month":"` + f.month + `","prompt":""}`
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- monthRequest(f.server.Handler(), "/month/generate", "application/json", body) }()
	<-entered
	rec := monthRequest(f.server.Handler(), "/month/generate", "application/json", body)
	if rec.Code != http.StatusConflict {
		t.Error("concurrent generation was not blocked")
	}
	if err := f.store.Mutate(func(d *models.Data) error { d.Entries[0].Hours++; return nil }); err != nil {
		t.Error(err)
	}
	before := f.store.Snapshot().Entries
	close(release)
	rec = <-done
	if rec.Code != http.StatusConflict || !reflect.DeepEqual(before, f.store.Snapshot().Entries) {
		t.Fatal("changed source accepted after inference")
	}
}

func TestMonthAIRejectsBadResponsesAndStaleWrites(t *testing.T) {
	t.Run("invalid response", func(t *testing.T) {
		f := newMonthAIFixture(t)
		before := f.store.Snapshot().Entries
		f.endpoint(t, func(*http.Request) string { return `{"entries":[{"date":"2099-01-01","projectId":"p","hours":8}]}` })
		rec := monthRequest(f.server.Handler(), "/month/generate", "application/json", `{"month":"`+f.month+`","prompt":""}`)
		if rec.Code != http.StatusBadGateway || !reflect.DeepEqual(before, f.store.Snapshot().Entries) {
			t.Fatal("invalid response was accepted or modified bookings")
		}
	})
	for _, mode := range []string{"changed", "moved", "expired", "persistence failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newMonthAIFixture(t)
			f.endpoint(t, func(*http.Request) string { answer, _ := json.Marshal(f.plan); return string(answer) })
			_, token := f.generate(t)
			want := http.StatusConflict
			switch mode {
			case "changed":
				if err := f.store.Mutate(func(d *models.Data) error { d.Entries[0].Hours++; return nil }); err != nil {
					t.Fatal(err)
				}
			case "moved":
				if err := f.store.Mutate(func(d *models.Data) error {
					d.Entries[0].Date = f.plan.Entries[1].Date
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "expired":
				p := f.server.monthAI.previews[token]
				p.Created = time.Now().Add(-monthPreviewTTL - time.Minute)
				f.server.monthAI.previews[token] = p
			case "persistence failure":
				want = http.StatusInternalServerError
				if err := os.Mkdir(f.store.Path()+".tmp", 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := f.store.Snapshot()
			rec := monthRequest(f.server.Handler(), "/month/save", "application/x-www-form-urlencoded", "preview="+token)
			if rec.Code != want || !reflect.DeepEqual(before, f.store.Snapshot()) {
				t.Fatalf("save %s: %d %s", mode, rec.Code, rec.Body.String())
			}
			if mode == "persistence failure" {
				if err := os.Remove(f.store.Path() + ".tmp"); err != nil {
					t.Fatal(err)
				}
				rec = monthRequest(f.server.Handler(), "/month/save", "application/x-www-form-urlencoded", "preview="+token)
				if rec.Code != http.StatusNoContent {
					t.Fatalf("retry after disk failure: %d %s", rec.Code, rec.Body.String())
				}
			}
		})
	}
}

func TestMonthAIPrivateAndPrompt(t *testing.T) {
	f := newMonthAIFixture(t)
	var calls atomic.Int32
	f.endpoint(t, func(*http.Request) string { calls.Add(1); return "{}" })
	before := f.store.Snapshot()
	for _, path := range []string{"/month/generate", "/month/save", "/month/prompt"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"month":"`+f.month+`"}`))
		req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		rec := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("private %s: %d", path, rec.Code)
		}
		req = httptest.NewRequest("POST", path, nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec = httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s: %d", path, rec.Code)
		}
	}
	if calls.Load() != 0 || !reflect.DeepEqual(before, f.store.Snapshot()) {
		t.Fatal("private/cross-site requests called the AI or modified data")
	}
	for _, prompt := range []string{"Eigener Planungsprompt", ""} {
		rec := monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", url.Values{"prompt": {prompt}}.Encode())
		if rec.Code != http.StatusNoContent || f.store.Snapshot().Settings.MonthPlanningPrompt != prompt {
			t.Fatal("prompt did not persist")
		}
	}
	rec := monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", url.Values{"prompt": {strings.Repeat("x", models.MaxMonthPlanningPrompt+1)}}.Encode())
	if rec.Code != http.StatusBadRequest {
		t.Fatal("oversized prompt accepted")
	}
}
