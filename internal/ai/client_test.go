package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAskEndpointModes(t *testing.T) {
	for _, mode := range []string{"classic", "v1-key", "v1-identity"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s", r.Method)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if mode == "classic" {
					if r.URL.Path != "/openai/deployments/custom-alias/chat/completions" || r.URL.Query().Get("api-version") != "2024-10-21" || body["model"] != nil {
						t.Errorf("invalid classic request: %s %s", r.URL, body["model"])
					}
				} else if r.URL.Path != "/openai/v1/chat/completions" || r.URL.RawQuery != "" || string(body["model"]) != `"custom-alias"` {
					t.Errorf("invalid v1 request: %s %s", r.URL, body["model"])
				}
				if mode == "v1-identity" {
					if r.Header.Get("api-key") != "" || r.Header.Get("Authorization") != "Bearer mock-token" || body["temperature"] != nil {
						t.Errorf("identity request contains wrong credentials or unsupported temperature")
					}
				} else if r.Header.Get("api-key") != "mock-key" || string(body["temperature"]) != "0" {
					t.Error("legacy key or sampling behavior changed")
				}
				if string(body["max_completion_tokens"]) != "8192" {
					t.Error("completion token limit missing")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Antwort"}}],"usage":{"total_tokens":9}}`)
			}))
			defer server.Close()
			cfg := Config{Endpoint: server.URL, Deployment: "custom-alias", APIKey: "mock-key"}
			if mode != "classic" {
				cfg.Endpoint += "/openai/v1/"
			}
			if mode == "v1-identity" {
				cfg.APIKey = ""
				cfg.Reasoning = true
				cfg.Authorize = func(r *http.Request) error {
					r.Header.Set("Authorization", "Bearer mock-token")
					return nil
				}
			}
			answer, err := Ask(context.Background(), cfg, "system", "user", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil || answer != "Antwort" {
				t.Fatalf("answer=%q error=%v", answer, err)
			}
		})
	}
}

func TestAskErrorsDoNotExposeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"status", "sensitive-token", 401},
		{"malformed", `{"sensitive-token":`, 200},
		{"missing", `{"error":{"message":"sensitive-token"}}`, 200},
		{"truncated", `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`, 200},
		{"filtered", `{"choices":[{"finish_reason":"content_filter","message":{"content":"partial"}}]}`, 200},
		{"oversized", strings.Repeat("x", (4<<20)+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			var logs bytes.Buffer
			answer, err := Ask(context.Background(), Config{Endpoint: server.URL, Deployment: "model", APIKey: "sensitive-token"}, "", "",
				slog.New(slog.NewTextHandler(&logs, nil)))
			if err == nil || answer != "" {
				t.Fatal("invalid response must fail explicitly")
			}
			if strings.Contains(err.Error()+logs.String(), "sensitive-token") {
				t.Fatal("credential or provider error body exposed")
			}
		})
	}
}

func TestAskRefusesRedirectsAndAuthFailures(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	cfg := Config{Endpoint: source.URL, Deployment: "model", APIKey: "sensitive-token"}
	if _, err := Ask(context.Background(), cfg, "", "", nil); err == nil || calls != 0 {
		t.Fatal("redirect forwarded credentials")
	}
	cfg.Authorize = func(*http.Request) error { return errors.New("sensitive-token") }
	_, err := Ask(context.Background(), cfg, "", "", nil)
	if err == nil || strings.Contains(err.Error(), "sensitive-token") {
		t.Fatal("authorization error leaked or fell back to the key")
	}
}

func TestChatURLValidation(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:secret@example.com", "https://example.com?api-key=secret", "https://example.com#secret",
		"https://example.com/a/../openai/v1", "https://example.com/%2e/openai/v1",
		"https://example.com/openai/v1/chat/completions", "file:///tmp/test",
	} {
		if _, _, err := ChatURL(endpoint, "model", ""); err == nil {
			t.Errorf("accepted invalid endpoint %s", endpoint)
		}
	}
	for _, deployment := range []string{"", "../model", "model?key=x", "model%2fother"} {
		if _, _, err := ChatURL("https://example.com", deployment, ""); err == nil {
			t.Errorf("accepted invalid deployment %q", deployment)
		}
	}
}
