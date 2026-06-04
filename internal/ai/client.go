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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

// Blueprint is a minimal but complete and valid example document. It is sent to
// the model as context so it knows the exact field names, nesting and value
// types it must produce – the remote endpoint has no other knowledge of the
// forecast JSON schema.
const Blueprint = `{
  "settings": {
    "year": 2027,
    "federalState": "SN",
    "weeklyTargetHours": 40,
    "fiscalYearStartMonth": 7,
    "ai": { "endpoint": "", "deployment": "", "apiVersion": "" }
  },
  "fiscalYears": {
    "2027": {
      "targetHours": 1440,
      "vacationDaysH1": 15,
      "vacationDaysH2": 15,
      "standardTaskLabel": "Standard Tasks",
      "standardTaskHours": 250
    }
  },
  "projects": [
    { "id": "proj-a", "name": "Projekt A", "budgetHours": 200, "color": "#2563eb", "active": true, "fiscalYear": 2027 }
  ],
  "entries": [
    { "date": "2026-07-01", "projectId": "proj-a", "hours": 8, "kind": "forecast" },
    { "date": "2026-07-02", "projectId": "proj-a", "hours": 8, "kind": "actual" }
  ],
  "forecastPlan": [
    { "projectId": "proj-a", "fiscalYear": 2027, "hoursPerWeek": 20, "kind": "forecast" }
  ]
}`

// systemPrompt instructs the model to return only the full JSON document.
const systemPrompt = `Du bist ein Assistent, der ein JSON-Dokument für ein Forecast-Tool bearbeitet.
Du erhältst das aktuelle JSON-Dokument und eine Anweisung des Nutzers.
Wende die Anweisung an und gib AUSSCHLIESSLICH das vollständige, gültige JSON-Dokument zurück.

Schema:
- settings: { year, federalState, weeklyTargetHours, fiscalYearStartMonth, ai{...} } – GLOBAL, NUR ändern wenn der Nutzer es ausdrücklich verlangt.
- fiscalYears: Objekt mit Jahr-Schlüsseln, je { targetHours, vacationDaysH1, vacationDaysH2, standardTaskLabel, standardTaskHours }.
- projects: Liste von { id, name, budgetHours, color, active, fiscalYear }. id = kurze eindeutige Kennung; color = Hex (#rrggbb); fiscalYear = das Anker-Jahr (FY 27 => 2027).
- entries: Liste von { date (YYYY-MM-DD), projectId, hours, kind } mit kind "forecast" (Plan) oder "actual" (Ist). Jede projectId MUSS zu einer projects.id passen.
- forecastPlan (OPTIONAL): kompakte Liste von { projectId, fiscalYear, hoursPerWeek, kind } für regelmäßige, über ein ganzes Fiskaljahr gleichmäßig verteilte Forecasts.

WICHTIG zur Vermeidung zu langer Antworten:
- Für „X Stunden pro Woche, gleichmäßig über das Fiskaljahr verteilt" NIEMALS die einzelnen Tageseinträge ausschreiben. Stattdessen GENAU EINEN Eintrag pro Projekt in "forecastPlan" anlegen: { projectId, fiscalYear, hoursPerWeek: X, kind: "forecast" }. Der Server expandiert das automatisch in Mo–Fr-Einträge (X/5 Stunden pro Werktag) für das gesamte Fiskaljahr.
- "entries" NUR für einzelne, konkret genannte Tage verwenden (z. B. „am 3. Juli 6 Stunden").

So sieht ein vollständiges, gültiges Dokument aus (Blueprint, exakt dieses Format und diese Feldnamen verwenden):
` + Blueprint + `

Regeln:
- Behalte alle bestehenden Daten bei, sofern die Anweisung nichts anderes verlangt. Ändere settings und andere Projekte nicht ohne Auftrag.
- Die Schlüssel in fiscalYears sind Strings (z. B. "2027"); fiscalYear in projects/forecastPlan ist eine Zahl (z. B. 2027).
- "FY 27" bzw. "Fiskaljahr 27" bedeutet fiscalYear 2027. Das FY beginnt am fiscalYearStartMonth (Standard Juli) des Anker-Jahres.
- budgetHours ist das Gesamtbudget des Projekts; verwechsle es nicht mit standardTaskHours.
Gib keinen erklärenden Text, keine Markdown-Codeblöcke und keine Kommentare aus – nur das reine JSON-Objekt.`

// Generate sends the prompt and current JSON to the configured endpoint and
// returns the model's JSON response (with any markdown fences stripped). It logs
// request/response metadata (never the API key) via the provided logger to ease
// debugging of the remote endpoint.
func Generate(ctx context.Context, cfg models.AISettings, prompt, currentJSON string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	deployment := strings.TrimSpace(cfg.Deployment)
	apiVersion := strings.TrimSpace(cfg.APIVersion)
	apiKey := strings.TrimSpace(cfg.APIKey)
	if endpoint == "" || deployment == "" || apiKey == "" {
		logger.Warn("ai request rejected: incomplete configuration",
			"endpointSet", endpoint != "", "deploymentSet", deployment != "", "apiKeySet", apiKey != "")
		return "", fmt.Errorf("KI-Endpoint ist nicht vollständig konfiguriert (Endpoint, Deployment und API-Key erforderlich)")
	}
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}

	url := strings.TrimRight(endpoint, "/") +
		"/openai/deployments/" + deployment +
		"/chat/completions?api-version=" + apiVersion

	reqBody := chatRequest{
		Temperature:         0,
		MaxCompletionTokens: 32768,
		ResponseFormat:      &responseFormat{Type: "json_object"},
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "Anweisung:\n" + prompt + "\n\nAktuelles JSON:\n" + currentJSON},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("Anfrage konnte nicht erstellt werden: %w", err)
	}

	logger.Info("ai request",
		"endpoint", endpoint, "deployment", deployment, "apiVersion", apiVersion,
		"promptChars", len(prompt), "inputJSONChars", len(currentJSON))

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("Anfrage konnte nicht erstellt werden: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error("ai endpoint unreachable", "error", err, "deployment", deployment)
		return "", fmt.Errorf("KI-Endpoint nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	elapsed := time.Since(start)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Error("ai endpoint error status",
			"status", resp.StatusCode, "deployment", deployment,
			"elapsedMs", elapsed.Milliseconds(), "body", snippet(body))
		return "", fmt.Errorf("KI-Endpoint antwortete mit %s: %s", resp.Status, snippet(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		logger.Error("ai response unreadable", "error", err, "body", snippet(body))
		return "", fmt.Errorf("KI-Antwort konnte nicht gelesen werden: %w", err)
	}
	if len(parsed.Choices) == 0 {
		logger.Error("ai response without choices", "body", snippet(body))
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
		return "", fmt.Errorf("KI-Antwort wurde abgeschnitten (Token-Limit erreicht). Formuliere den Prompt kompakter oder fordere weniger Einträge an (z. B. Stunden pro Woche statt pro Tag).")
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

// snippet shortens a response body for error messages.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

type chatRequest struct {
	Messages            []chatMessage   `json:"messages"`
	Temperature         float64         `json:"temperature"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	ResponseFormat      *responseFormat `json:"response_format,omitempty"`
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
