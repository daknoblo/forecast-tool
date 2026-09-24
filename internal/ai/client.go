// Package ai talks to a remote, Azure OpenAI-compatible chat-completions
// endpoint (e.g. an Azure AI Foundry model-router) so the user can ask questions
// about their own forecast and booking figures.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is the effective, request-scoped configuration. Authorize is supplied
// by the Foundry identity client; credentials are never persisted here.
type Config struct {
	Endpoint, Deployment, APIVersion, APIKey string
	Authorize                                func(*http.Request) error
	Reasoning                                bool
}

// requestTimeout bounds a single AI call. Model routers can be slow, but a
// request must not hang a UI handler forever.
const requestTimeout = 120 * time.Second

// httpClient is used instead of http.DefaultClient so redirects are refused:
// the secret key travels in a custom "api-key" header, which Go would happily
// forward to whatever host a redirect points at.
var httpClient = &http.Client{
	Timeout: requestTimeout + 30*time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Weiterleitung abgelehnt (der API-Key darf nicht an ein anderes Ziel gesendet werden)")
	},
}

// Ask sends a system and a user message to the configured chat endpoint and
// returns the model's plain-text answer. It logs request/response metadata
// (never the API key) via the provided logger to ease debugging of the remote
// endpoint.
func Ask(ctx context.Context, cfg Config, system, user string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	deployment := strings.TrimSpace(cfg.Deployment)
	apiVersion := strings.TrimSpace(cfg.APIVersion)
	apiKey := strings.TrimSpace(cfg.APIKey)
	if endpoint == "" || deployment == "" || (apiKey == "" && cfg.Authorize == nil) {
		logger.Warn("ai request rejected: incomplete configuration",
			"endpointSet", endpoint != "", "deploymentSet", deployment != "", "apiKeySet", apiKey != "")
		return "", fmt.Errorf("KI-Endpoint ist nicht vollständig konfiguriert (Endpoint, Deployment und Zugangsdaten erforderlich)")
	}
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}

	target, v1, err := ChatURL(endpoint, deployment, apiVersion)
	if err != nil {
		return "", err
	}

	reqBody := chatRequest{
		MaxCompletionTokens: 8192,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	if !cfg.Reasoning {
		temperature := 0.0
		reqBody.Temperature = &temperature
	}
	if v1 {
		reqBody.Model = deployment
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("Anfrage konnte nicht erstellt werden: %w", err)
	}

	logger.Info("ai request",
		"endpoint", endpoint, "deployment", deployment, "apiVersion", apiVersion,
		"systemChars", len(system), "userChars", len(user))

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("Anfrage konnte nicht erstellt werden: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Authorize != nil {
		if err := cfg.Authorize(req); err != nil {
			logger.Error("ai authorization failed")
			return "", fmt.Errorf("KI-Anmeldung fehlgeschlagen. Prüfe die Foundry-Einstellungen und Azure-Berechtigungen.")
		}
	} else {
		req.Header.Set("api-key", apiKey)
	}

	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("ai endpoint unreachable", "error", err, "deployment", deployment)
		return "", fmt.Errorf("KI-Endpoint nicht erreichbar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	const maxBody = 4 << 20
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	elapsed := time.Since(start)
	if readErr != nil || len(body) > maxBody {
		logger.Error("ai response body could not be read", "oversized", len(body) > maxBody)
		return "", fmt.Errorf("KI-Antwort konnte nicht vollständig gelesen werden oder ist zu groß")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Error("ai endpoint error status",
			"status", resp.StatusCode, "deployment", deployment,
			"elapsedMs", elapsed.Milliseconds())
		return "", fmt.Errorf("KI-Endpoint antwortete mit HTTP %d. Prüfe Deployment, Zugangsdaten und Azure-Berechtigungen.", resp.StatusCode)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		logger.Error("ai response unreadable")
		return "", fmt.Errorf("KI-Antwort konnte nicht gelesen werden")
	}
	if len(parsed.Choices) == 0 {
		logger.Error("ai response without choices")
		return "", fmt.Errorf("KI-Antwort enthielt kein Ergebnis")
	}
	finish := parsed.Choices[0].FinishReason
	logger.Info("ai response",
		"finishReason", finish, "elapsedMs", elapsed.Milliseconds(),
		"promptTokens", parsed.Usage.PromptTokens,
		"completionTokens", parsed.Usage.CompletionTokens,
		"totalTokens", parsed.Usage.TotalTokens,
		"contentChars", len(parsed.Choices[0].Message.Content))
	if finish == "length" {
		logger.Warn("ai response truncated (token limit)",
			"completionTokens", parsed.Usage.CompletionTokens, "deployment", deployment)
		return "", fmt.Errorf("KI-Antwort wurde abgeschnitten (Token-Limit erreicht). Stelle eine engere Frage.")
	}
	if finish == "content_filter" {
		return "", fmt.Errorf("KI-Antwort wurde durch den Inhaltsfilter blockiert")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		logger.Error("ai response empty content", "finishReason", finish)
		return "", fmt.Errorf("KI-Antwort war leer")
	}
	return stripCodeFences(content), nil
}

// stripCodeFences removes surrounding ```json ... ``` markers if present.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// drop an optional language hint on the first line (e.g. "json")
		if !strings.Contains(s[:i], "{") {
			s = s[i+1:]
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

type chatRequest struct {
	Model               string        `json:"model,omitempty"`
	Messages            []chatMessage `json:"messages"`
	Temperature         *float64      `json:"temperature,omitempty"`
	MaxCompletionTokens int           `json:"max_completion_tokens,omitempty"`
}

// ChatURL supports explicit OpenAI v1 base URLs without changing legacy
// resource-root configurations. Identity mode always provides a verified v1 URL.
func ChatURL(endpoint, deployment, apiVersion string) (string, bool, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.Contains(u.EscapedPath(), "%") {
		return "", false, fmt.Errorf("KI-Endpoint muss eine absolute HTTP(S)-Basis-URL ohne Zugangsdaten, Query oder Fragment sein")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", false, fmt.Errorf("KI-Endpoint enthält einen ungültigen Pfad")
		}
	}
	if deployment == "" || len(deployment) > 100 || strings.ContainsAny(deployment, "/\\?#%") ||
		deployment == "." || deployment == ".." {
		return "", false, fmt.Errorf("Ungültiger KI-Deployment-Name")
	}
	if strings.HasSuffix(u.Path, "/openai/v1") {
		u.Path += "/chat/completions"
		return u.String(), true, nil
	}
	if strings.Contains(u.Path, "/openai/") {
		return "", false, fmt.Errorf("Verwende die Azure-Ressourcen-URL oder eine Basis-URL mit /openai/v1")
	}
	u.Path += "/openai/deployments/" + deployment + "/chat/completions"
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}
	u.RawQuery = url.Values{"api-version": {apiVersion}}.Encode()
	return u.String(), false, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}
