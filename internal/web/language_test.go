package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/i18n"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

func TestLanguageSettingsAndPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	store, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	post := func(language string) *httptest.ResponseRecorder {
		form := url.Values{"section": {"language"}, "language": {language}}
		request := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-Requested-With", "fetch")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if store.Snapshot().Settings.Language != "de" {
		t.Fatal("new stores must default to German")
	}
	for _, language := range []string{"en", "de"} {
		if result := post(language); result.Code != http.StatusNoContent {
			t.Fatalf("language save: %d %s", result.Code, result.Body.String())
		}
		for _, route := range []string{"/", "/projects", "/month", "/goal", "/settings"} {
			t.Run(language+route, func(t *testing.T) {
				result := httptest.NewRecorder()
				handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, route, nil))
				if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `<html lang="`+language+`">`) {
					t.Fatalf("page locale: %d %s", result.Code, result.Body.String())
				}
				if result.Header().Get("Content-Language") != language {
					t.Fatal("missing content language")
				}
				label, unwanted := "Settings", "Einstellungen"
				if language == "de" {
					label, unwanted = unwanted, label
				}
				if !strings.Contains(result.Body.String(), ">"+label+"</a>") || strings.Contains(result.Body.String(), ">"+unwanted+"</a>") {
					t.Fatal("navigation does not match selected language")
				}
			})
		}
		reopened, err := storage.New(path)
		if err != nil || reopened.Snapshot().Settings.Language != language {
			t.Fatalf("language was not persisted: %v", err)
		}
	}
	if result := post("fr"); result.Code != http.StatusBadRequest || store.Snapshot().Settings.Language != "de" {
		t.Fatal("invalid language changed settings")
	}
	if result := post(""); result.Code != http.StatusBadRequest {
		t.Fatal("empty language was accepted")
	}
}

func TestEnglishPreservesUserContentAndPrivateMode(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Mutate(func(data *models.Data) error {
		data.Settings.Language = "en"
		data.Settings.Utilization.HighLabel = "Eigene Beschriftung"
		data.Projects = append(data.Projects, models.Project{
			ID: "custom", AssignmentID: "custom", Name: "Urlaub", FiscalYear: data.Settings.Year,
			BudgetHours: 100, Active: true, Color: "#123456",
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	server.Handler().ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if !strings.Contains(result.Body.String(), `value="Urlaub"`) {
		t.Fatal("user project name must not be translated")
	}
	result = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/settings", nil)
	request.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	server.Handler().ServeHTTP(result, request)
	if !strings.Contains(result.Body.String(), `<html lang="en">`) || !strings.Contains(result.Body.String(), `value="Eigene Beschriftung"`) {
		t.Fatal("private mode lost language or changed a custom label")
	}
	result = httptest.NewRecorder()
	server.Handler().ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/month?month=invalid", nil))
	if result.Code != http.StatusBadRequest || strings.Contains(result.Body.String(), "Ungültiger") {
		t.Fatalf("error was not localized: %s", result.Body.String())
	}
}

func TestLanguageLegacyDocumentAndAPI(t *testing.T) {
	t.Setenv("FORECAST_API_WRITE_TOKEN", "language-test-token")
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte(`{"settings":{"year":2027},"projects":[],"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.New(path)
	if err != nil || store.Snapshot().Settings.Language != "de" {
		t.Fatalf("legacy language default: %v", err)
	}
	server, err := NewServer(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"language":"en"}`, http.StatusOK},
		{`{"language":"xx"}`, http.StatusBadRequest},
		{`{"language":""}`, http.StatusBadRequest},
		{`{"weeklyTargetHours":32}`, http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer language-test-token")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != tc.code || store.Snapshot().Settings.Language != "en" {
			t.Fatalf("API language update %s: %d %s", tc.body, response.Code, response.Body.String())
		}
	}

}

func TestEnglishPlanningDefaultsPreserveCustomPrompts(t *testing.T) {
	data := models.DefaultData(2027)
	data.Settings.Language = "en"
	for _, text := range []string{monthPrompt(data), monthSystemPrompt(data), i18n.Text("en", forecast.MonthPlanningExplanationFormat)} {
		if strings.Contains(text, "auf Deutsch") || strings.Contains(text, "deutsche Planungsübersicht") {
			t.Fatal("English planning still requests German output")
		}
	}
	if monthPrompt(data) == forecast.DefaultMonthPlanningPrompt || monthSystemPrompt(data) == forecast.MonthPlanningSystemPrompt {
		t.Fatal("built-in planning prompts were not translated")
	}
	for _, preset := range localizedChatPresets("en") {
		if strings.Contains(preset.Label, "Projekte") || strings.Contains(preset.Prompt, "Fiskaljahr") {
			t.Fatalf("chat preset was not translated: %+v", preset)
		}
	}
	if !strings.Contains(i18n.Text("en", chatSystemPrompt), "English") {
		t.Fatal("goal chat does not request English")
	}
	data.Settings.MonthPlanningPrompt = "Mein unveränderter Prompt"
	data.Settings.MonthPlanningSystemPrompt = "Mein eigenes Antwortformat"
	if monthPrompt(data) != data.Settings.MonthPlanningPrompt || monthSystemPrompt(data) != data.Settings.MonthPlanningSystemPrompt {
		t.Fatal("custom prompts were translated or overwritten")
	}
}

func TestEnglishCalendarAndChartLabels(t *testing.T) {
	data := models.DefaultData(2027)
	data.Settings.Language = "en"
	data.Projects = []models.Project{{
		ID: "p", AssignmentID: "p", Name: "Monate", FiscalYear: 2027,
		BudgetHours: 100, Active: true, Color: "#123456",
	}}
	data.Entries = []models.Entry{{Date: "2026-11-02", ProjectID: "p", Hours: 8}}
	cal := holidays.Get(2027, "SN")
	flow := goalFlowSVG(forecast.BuildGoalFlow(data, cal), "en")
	for _, expected := range []string{">Projects<", ">Months<", "Half-year 1", "Monate"} {
		if !strings.Contains(string(flow), expected) {
			t.Errorf("missing localized chart label or original project name %q", expected)
		}
	}
	date := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	plan := forecast.BuildMonthPlan(data, cal, date, date)
	if plan.Label != "November 2026" || !strings.HasPrefix(plan.Weeks[0].Days[0].Label, "Monday ") {
		t.Fatal("calendar month or weekday label was not localized")
	}
	found := false
	for _, week := range plan.Weeks {
		for _, day := range week.Days {
			if day.Date == "2026-11-18" {
				found = day.Holiday != "" && !strings.Contains(day.Holiday, "Buß")
			}
		}
	}
	if !found {
		t.Fatal("Saxony holiday was missing or not translated")
	}
}
