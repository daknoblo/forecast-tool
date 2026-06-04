// Package ai talks to a remote, Azure OpenAI-compatible chat-completions
// endpoint (e.g. an Azure AI Foundry model-router) to update the forecast
// JSON document from a natural-language prompt.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

// systemPrompt instructs the model to return only the full JSON document.
const systemPrompt = `Du bist ein Assistent, der ein JSON-Dokument für ein Forecast-Tool bearbeitet.
Du erhältst das aktuelle JSON-Dokument und eine Anweisung des Nutzers.
Wende die Anweisung an und gib AUSSCHLIESSLICH das vollständige, gültige JSON-Dokument zurück.
Behalte alle bestehenden Daten bei, sofern die Anweisung nichts anderes verlangt.
Gib keinen erklärenden Text, keine Markdown-Codeblöcke und keine Kommentare aus – nur das reine JSON-Objekt.`

// Generate sends the prompt and current JSON to the configured endpoint and
// returns the model's JSON response (with any markdown fences stripped).
func Generate(ctx context.Context, cfg models.AISettings, prompt, currentJSON string) (string, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	deployment := strings.TrimSpace(cfg.Deployment)
	apiVersion := strings.TrimSpace(cfg.APIVersion)
	apiKey := strings.TrimSpace(cfg.APIKey)
	if endpoint == "" || deployment == "" || apiKey == "" {
		return "", fmt.Errorf("KI-Endpoint ist nicht vollständig konfiguriert (Endpoint, Deployment und API-Key erforderlich)")
	}
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}

	url := strings.TrimRight(endpoint, "/") +
		"/openai/deployments/" + deployment +
		"/chat/completions?api-version=" + apiVersion

	reqBody := chatRequest{
		Temperature:    0,
		ResponseFormat: &responseFormat{Type: "json_object"},
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "Anweisung:\n" + prompt + "\n\nAktuelles JSON:\n" + currentJSON},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("Anfrage konnte nicht erstellt werden: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("Anfrage konnte nicht erstellt werden: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("KI-Endpoint nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("KI-Endpoint antwortete mit %s: %s", resp.Status, snippet(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("KI-Antwort konnte nicht gelesen werden: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("KI-Antwort enthielt kein Ergebnis")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
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

// snippet shortens a response body for error messages.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

type chatRequest struct {
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}
