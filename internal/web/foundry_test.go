package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/foundry"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
)

type mockFoundry struct {
	snapshot foundry.Snapshot
	err      error
	refresh  int
	auth     int
}

func (m *mockFoundry) Refresh(context.Context) (foundry.Snapshot, error) {
	m.refresh++
	return m.snapshot, m.err
}

func (m *mockFoundry) Authorize(r *http.Request) error {
	m.auth++
	r.Header.Del("api-key")
	r.Header.Set("Authorization", "Bearer test-identity-token")
	return nil
}

func foundryTestServer(t *testing.T) (*Server, *storage.Store, *mockFoundry) {
	t.Helper()
	for _, name := range []string{foundryResourceEnv, foundryTenantEnv, foundryClientEnv, foundrySecretEnv, aiAPIKeyEnv} {
		t.Setenv(name, "")
	}
	_, store := newTestServer(t)
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockFoundry{snapshot: foundry.Snapshot{
		Endpoint: "https://verified.openai.azure.com/openai/v1", RefreshedAt: time.Now(),
		Deployments: []foundry.Deployment{
			{Name: "analyst", ModelName: "gpt-5", ModelFormat: "OpenAI", ProvisioningState: "Succeeded", Reasoning: true},
		},
	}}
	srv.foundry = &foundryState{enabled: true, source: mock, gate: make(chan struct{}, 1), resourceID: "test-resource"}
	return srv, store, mock
}

func TestSettingsFoundryPresentation(t *testing.T) {
	srv, _, mock := foundryTestServer(t)
	for _, tc := range []struct {
		name                        string
		enabled, secretSet, private bool
		discoveryError              bool
	}{
		{name: "manual"},
		{name: "foundry set", enabled: true, secretSet: true},
		{name: "foundry unset", enabled: true},
		{name: "foundry error", enabled: true, secretSet: true, discoveryError: true},
		{name: "private", enabled: true, secretSet: true, private: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZURE_RESOURCE_ID", "test-resource")
			t.Setenv("AZURE_TENANT_ID", "test-tenant")
			t.Setenv("AZURE_CLIENT_ID", "test-client")
			secret := ""
			if tc.secretSet {
				secret = "secret-must-never-appear-in-html"
			}
			t.Setenv("AZURE_CLIENT_SECRET", secret)
			srv.foundry = newFoundryState()
			if srv.foundry.secretSet != tc.secretSet {
				t.Fatal("secret presence does not match the environment")
			}
			srv.foundry.enabled = tc.enabled
			if !tc.discoveryError {
				srv.foundry.setupErr = nil
				srv.foundry.source = mock
			}
			req := httptest.NewRequest("GET", "/settings", nil)
			if tc.private {
				req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("settings status = %d", rec.Code)
			}
			if strings.Contains(strings.ToLower(body), "automatisch gespeichert") {
				t.Fatal("settings still contain automatic-save hints")
			}
			for _, removed := range []string{
				"Befüllt",
				"Microsoft Foundry · Entra-ID", "Die Anmeldung erfolgt per Client-Secret",
				"Erforderlich sind Leserechte", "automatische Aktualisierung nach 5 Minuten",
				`href="/goal#chat"`, `href="https://github.com/daknoblo/forecast-tool#chat-with-your-data"`,
				"<code>AZURE_RESOURCE_ID</code>", "<code>AZURE_TENANT_ID</code>",
				"<code>AZURE_CLIENT_ID</code>", "<code>AZURE_CLIENT_SECRET</code>",
			} {
				if strings.Contains(body, removed) {
					t.Errorf("settings still contain redundant text: %s", removed)
				}
			}
			if strings.Count(body, "data-save-quiet hidden") != strings.Count(body, "data-autosave>") {
				t.Fatal("settings forms must retain hidden auto-save feedback")
			}
			if secret != "" && strings.Contains(body, secret) {
				t.Fatal("settings exposed the secret")
			}
			if tc.enabled && !tc.private {
				status := `<code class="secret-status">Nicht gesetzt</code>`
				if tc.secretSet {
					status = `<code class="secret-status ok">gesetzt</code>`
				}
				for _, want := range []string{
					`class="kv tokens foundry-config"`,
					"<td>Azure-Ressource</td>", "<td>Tenant-ID</td>", "<td>Client-ID</td>",
					"<td>Erkannter Endpoint</td>", "<td>Client-Secret</td>", "<td>" + status + "</td>",
					"<code>test-resource</code>", "<code>test-tenant</code>", "<code>test-client</code>",
					`class="form-row foundry-controls"`, `form="foundry-refresh"`,
					`action="/settings/ai/refresh" id="foundry-refresh"`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("settings missing %s", want)
					}
					endpoint := mock.snapshot.Endpoint
					if tc.discoveryError {
						endpoint = "Nicht verfügbar"
					}
					if !strings.Contains(body, "<code>"+endpoint+"</code>") {
						t.Fatal("endpoint is not shown in the value field")
					}
				}
			}
			if tc.private && (strings.Contains(body, "<td>Client-Secret</td>") || srv.foundrySettings(req, "").SecretSet) {
				t.Fatal("private mode exposed secret presence")
			}
		})
	}
}

func TestSettingsSecretPresenceFields(t *testing.T) {
	srv, _, _ := foundryTestServer(t)
	srv.foundry = newFoundryState()
	for _, tc := range []struct {
		name             string
		read, write, key bool
	}{
		{name: "empty"},
		{name: "all set", read: true, write: true, key: true},
		{name: "read only", read: true},
		{name: "write only", write: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, set := range map[string]bool{
				"FORECAST_API_READ_TOKEN": tc.read, "FORECAST_API_WRITE_TOKEN": tc.write, aiAPIKeyEnv: tc.key,
			} {
				value := ""
				if set {
					value = name + "-secret-must-stay-hidden"
				}
				t.Setenv(name, value)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
			body := rec.Body.String()
			if rec.Code != http.StatusOK || strings.Contains(body, "-secret-must-stay-hidden") {
				t.Fatal("settings failed to render or exposed a secret")
			}
			for label, set := range map[string]bool{"Lese-Token": tc.read, "Schreib-Token": tc.write} {
				_, rest, found := strings.Cut(body, "<td>"+label+"</td>")
				row, _, _ := strings.Cut(rest, "</tr>")
				status := `<code class="secret-status">Nicht gesetzt</code>`
				if set {
					status = `<code class="secret-status ok">gesetzt</code>`
				}
				if !found || !strings.Contains(row, status) || strings.Contains(row, "FORECAST_API_") {
					t.Errorf("%s must show only its presence with green styling when set", label)
				}
			}
			keyStatus := `<code class="secret-status">Nicht gesetzt</code>`
			if tc.key {
				keyStatus = `<code class="secret-status ok">gesetzt</code>`
			}
			_, keyRest, keyFound := strings.Cut(body, "<label>API-Key")
			keyField, _, _ := strings.Cut(keyRest, "</label>")
			if !keyFound || !strings.Contains(keyField, keyStatus) {
				t.Fatal("manual API key must show only its presence with green styling when set")
			}
		})
	}
}

func TestFoundrySettingsAndSelection(t *testing.T) {
	srv, store, mock := foundryTestServer(t)
	if err := store.Mutate(func(d *models.Data) error {
		d.Settings.AI = models.AISettings{Endpoint: "https://legacy.openai.azure.com", APIVersion: "2024-10-21"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "analyst · gpt-5") || !strings.Contains(rec.Body.String(), mock.snapshot.Endpoint) {
			t.Fatalf("missing discovered model/endpoint: %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), `name="aiEndpoint"`) {
			t.Fatal("identity mode must not expose a writable endpoint override")
		}
	}
	if mock.refresh != 1 {
		t.Fatal("settings did not cache discovery")
	}
	post := func(deployment string) int {
		t.Helper()
		req := httptest.NewRequest("POST", "/settings", strings.NewReader("section=ai&aiDeployment="+deployment+"&aiEndpoint=https://evil.invalid"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Requested-With", "fetch")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post("analyst"); got != 204 {
		t.Fatalf("selection save = %d", got)
	}
	settings := store.Snapshot().Settings.AI
	if settings.Deployment != "analyst" || settings.Endpoint != "https://legacy.openai.azure.com" || settings.APIKey != "" {
		t.Fatalf("selection overwrote manual config or persisted credentials: %+v", settings)
	}
	if got := post("unavailable"); got != 400 || store.Snapshot().Settings.AI.Deployment != "analyst" {
		t.Fatal("unknown model selection modified storage")
	}
	if err := os.Mkdir(store.Path()+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if got := post("analyst"); got != 500 {
		t.Fatalf("persistence failure returned success: %d", got)
	}
}

func TestFoundryCacheFailsClosedAndHonorsCancellation(t *testing.T) {
	srv, _, mock := foundryTestServer(t)
	ctx := context.Background()
	if _, err := srv.foundry.catalog(ctx, false); err != nil {
		t.Fatal(err)
	}
	mock.err = errors.New("Testfehler beim Laden")
	if snapshot, err := srv.foundry.catalog(ctx, true); err == nil || snapshot.Endpoint != "" {
		t.Fatal("failed refresh retained the old endpoint")
	}
	if _, err := srv.aiConfig(ctx, models.AISettings{Deployment: "analyst", APIKey: "legacy-key", Endpoint: "https://legacy.invalid"}); err == nil {
		t.Fatal("discovery error fell back to a key or stale catalog")
	}
	if mock.refresh != 2 {
		t.Fatal("failed discovery was immediately retried")
	}
	srv.foundry.gate <- struct{}{}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := srv.foundry.catalog(cancelled, true); err == nil {
		t.Fatal("cancelled waiter was not released")
	}
	<-srv.foundry.gate
}

func TestFoundryPrivateModeMakesNoCalls(t *testing.T) {
	srv, _, mock := foundryTestServer(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/settings", "", 200},
		{"POST", "/settings/ai/refresh", "", 403},
		{"POST", "/settings", "section=ai&aiDeployment=analyst", 403},
		{"POST", "/goal/chat", `{"prompt":"Test"}`, 403},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "test-resource") || strings.Contains(rec.Body.String(), mock.snapshot.Endpoint) {
			t.Fatal("private settings exposed the Foundry resource")
		}
	}
	if mock.refresh != 0 || mock.auth != 0 {
		t.Fatal("private mode accessed Azure")
	}
}

func TestFoundryChatUsesDiscoveredEndpointAndIdentity(t *testing.T) {
	srv, store, mock := foundryTestServer(t)
	var called atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		if r.URL.Path != "/openai/v1/chat/completions" || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer test-identity-token" || r.Header.Get("api-key") != "" {
			t.Error("inference request did not use the discovered v1 identity binding")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if string(body["model"]) != `"analyst"` || body["temperature"] != nil {
			t.Error("canonical reasoning model was treated as its arbitrary deployment alias")
		}
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Testantwort"}}]}`)
	}))
	defer endpoint.Close()
	mock.snapshot.Endpoint = endpoint.URL + "/openai/v1"
	if err := store.Mutate(func(d *models.Data) error {
		d.Settings.AI = models.AISettings{Endpoint: "https://never-contact.invalid", Deployment: "analyst"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/goal/chat", strings.NewReader(`{"prompt":"Wie ist meine Auslastung?"}`)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Testantwort") || !called.Load() || mock.auth != 1 {
		t.Fatalf("chat failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFoundryAzureEnvironment(t *testing.T) {
	env := map[string]string{
		"AZURE_RESOURCE_ID":   "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/test/providers/Microsoft.CognitiveServices/accounts/test",
		"AZURE_TENANT_ID":     "22222222-2222-2222-2222-222222222222",
		"AZURE_CLIENT_ID":     "33333333-3333-3333-3333-333333333333",
		"AZURE_CLIENT_SECRET": "test-only-secret",
	}
	for name, value := range env {
		t.Setenv(name, value)
	}
	state := newFoundryState()
	if !state.enabled || state.setupErr != nil || state.source == nil {
		t.Fatalf("AZURE_* configuration was not loaded: %v", state.setupErr)
	}
	if state.resourceID != env["AZURE_RESOURCE_ID"] || state.tenantID != env["AZURE_TENANT_ID"] || state.clientID != env["AZURE_CLIENT_ID"] {
		t.Fatal("Foundry identity does not match the AZURE_* configuration")
	}
	for name := range env {
		t.Run("missing "+name, func(t *testing.T) {
			t.Setenv(name, "")
			state := newFoundryState()
			if !state.enabled || state.setupErr == nil {
				t.Fatal("incomplete AZURE_* configuration did not fail closed")
			}
		})
	}
	t.Run("all empty", func(t *testing.T) {
		for name := range env {
			t.Setenv(name, "")
		}
		state := newFoundryState()
		if state.enabled || state.setupErr != nil || state.source != nil {
			t.Fatal("empty AZURE_* configuration did not preserve manual mode")
		}
	})
}

func TestIncompleteFoundryDoesNotUseManualCredentials(t *testing.T) {
	for _, name := range []string{foundryResourceEnv, foundryTenantEnv, foundryClientEnv, foundrySecretEnv} {
		t.Setenv(name, "")
	}
	t.Setenv(foundryResourceEnv, "invalid-resource")
	t.Setenv(aiAPIKeyEnv, "manual-key")
	_, store := newTestServer(t)
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := models.AISettings{Endpoint: "https://manual.invalid", Deployment: "manual", APIKey: "legacy-key"}
	if !srv.foundry.enabled || srv.foundry.setupErr == nil || srv.aiReady(settings) {
		t.Fatal("partial Foundry configuration did not fail closed")
	}
	if _, err := srv.aiConfig(context.Background(), settings); err == nil {
		t.Fatal("invalid identity fell back to manual credentials")
	}
}

func TestFoundryRefreshRouteAndMissingSelection(t *testing.T) {
	srv, _, mock := foundryTestServer(t)
	h := srv.Handler()
	call := func(crossSite bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/settings/ai/refresh", nil)
		if crossSite {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(true); rec.Code != 403 || mock.refresh != 0 {
		t.Fatal("cross-site request refreshed discovery")
	}
	if rec := call(false); rec.Code != 303 || rec.Header().Get("Location") != "/settings#ai" || mock.refresh != 1 {
		t.Fatal("explicit refresh did not load the catalog")
	}
	mock.snapshot.Deployments = nil
	if rec := call(false); rec.Code != 303 || mock.refresh != 2 {
		t.Fatal("forced refresh incorrectly reused cache")
	}
	if _, err := srv.aiConfig(context.Background(), models.AISettings{Deployment: "analyst"}); err == nil {
		t.Fatal("deleted deployment remained usable")
	}
	mock.err = errors.New("Kein Zugriff auf die Ressource")
	if rec := call(false); rec.Code != 503 || !strings.Contains(rec.Body.String(), "Kein Zugriff") {
		t.Fatal("refresh failure was not visible")
	}
}

func TestManualAISettingsRemainCompatible(t *testing.T) {
	srv, store, _ := foundryTestServer(t)
	srv.foundry = newFoundryState()
	h := srv.Handler()
	for _, endpoint := range []string{"https://legacy.openai.azure.com", "https://new.services.ai.azure.com/openai/v1"} {
		req := httptest.NewRequest("POST", "/settings", strings.NewReader("section=ai&aiEndpoint="+endpoint+"&aiDeployment=router&aiApiVersion=2024-10-21"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Requested-With", "fetch")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 || store.Snapshot().Settings.AI.Endpoint != endpoint {
			t.Fatalf("manual endpoint save failed: %d %s", rec.Code, rec.Body.String())
		}
	}
	t.Setenv(aiAPIKeyEnv, "manual-test-key")
	cfg, err := srv.aiConfig(context.Background(), store.Snapshot().Settings.AI)
	if err != nil || cfg.APIKey != "manual-test-key" || cfg.Authorize != nil {
		t.Fatal("manual credentials no longer work")
	}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader("section=ai&aiEndpoint=https://user:secret@example.com&aiDeployment=router"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 || strings.Contains(store.Snapshot().Settings.AI.Endpoint, "secret") {
		t.Fatal("credential-bearing endpoint was accepted")
	}
}
