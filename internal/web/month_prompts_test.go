package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestMonthSystemPromptConfiguration(t *testing.T) {
	f := newMonthAIFixture(t)
	system := "Antworte in JSON. <Eigene Systemregeln>"
	form := url.Values{"prompt": {"Eigene Planung"}, "systemPrompt": {system}}
	rec := monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", form.Encode())
	if rec.Code != http.StatusNoContent || f.store.Snapshot().Settings.MonthPlanningSystemPrompt != system {
		t.Fatalf("save system prompt: %d %s", rec.Code, rec.Body.String())
	}
	// Older clients editing only the task prompt must preserve the system prompt.
	rec = monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", "prompt=Planung")
	if rec.Code != http.StatusNoContent || f.store.Snapshot().Settings.MonthPlanningSystemPrompt != system {
		t.Fatal("legacy prompt save erased system configuration")
	}
	for _, private := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/month?month="+f.month, nil)
		if private {
			req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		}
		rec = httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `name="systemPrompt"`) {
			t.Fatal("missing editable system prompt")
		}
		for _, want := range []string{`<details class="card month-method">`, "<summary>Prompts</summary>", "<details open>", `class="prompt" rows="1"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("prompt area must start collapsed with expanded contents and content-sized fields: %s", want)
			}
		}
		if strings.Contains(body, html.EscapeString(system)) == private {
			t.Fatal("custom system prompt visibility is wrong")
		}
	}
	before := f.store.Snapshot()
	form.Set("prompt", "Must not save")
	form.Set("systemPrompt", strings.Repeat("ä", models.MaxMonthPlanningPrompt+1))
	rec = monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", form.Encode())
	if rec.Code != http.StatusBadRequest || !reflect.DeepEqual(before, f.store.Snapshot()) {
		t.Fatal("oversized system prompt changed configuration")
	}
	raw, _ := json.Marshal(map[string]string{
		"month": f.month, "prompt": "Must not save", "systemPrompt": form.Get("systemPrompt"),
	})
	rec = monthRequest(f.server.Handler(), "/month/generate", "application/json", string(raw))
	if rec.Code != http.StatusBadRequest || !reflect.DeepEqual(before, f.store.Snapshot()) {
		t.Fatal("generation accepted oversized system prompt or changed configuration")
	}
	form.Set("systemPrompt", strings.Repeat("ä", models.MaxMonthPlanningPrompt))
	form.Set("prompt", strings.Repeat("ä", models.MaxMonthPlanningPrompt))
	rec = monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", form.Encode())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid Unicode prompts rejected by request size limit: %d", rec.Code)
	}
	form.Set("systemPrompt", "")
	form.Set("prompt", "")
	rec = monthRequest(f.server.Handler(), "/month/prompt", "application/x-www-form-urlencoded", form.Encode())
	d := f.store.Snapshot()
	if rec.Code != http.StatusNoContent || d.Settings.MonthPlanningSystemPrompt != "" ||
		monthSystemPrompt(d) != forecast.MonthPlanningSystemPrompt || monthPrompt(d) != forecast.DefaultMonthPlanningPrompt {
		t.Fatal("empty configuration did not restore defaults")
	}
}

func TestMonthGenerateConfiguredSystemPrompt(t *testing.T) {
	for _, want := range []string{
		"ausschließlich die getroffenen Planungsentscheidungen",
		"genau einem Stichpunkt je geplantem Projekt",
		"geplanten Tagen bzw. Zeiträumen",
		"Keine Erklärungen, historischen Muster",
		"Vorrang vor anderen Aufforderungen",
		"unallocated.reason",
	} {
		if !strings.Contains(forecast.MonthPlanningExplanationFormat, want) {
			t.Fatalf("missing decision-only overview instruction: %s", want)
		}
	}
	for _, mode := range []string{"saved", "submitted", "default", "invalid-plan"} {
		t.Run(mode, func(t *testing.T) {
			f := newMonthAIFixture(t)
			if err := f.store.Mutate(func(d *models.Data) error {
				d.Settings.MonthPlanningSystemPrompt = "Saved system rules"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			body := map[string]string{"month": f.month, "prompt": "Task instructions"}
			expected := "Saved system rules"
			switch mode {
			case "submitted", "invalid-plan":
				expected = "Custom system rules, including permission to invent hours"
				body["systemPrompt"] = expected
			case "default":
				expected = forecast.MonthPlanningSystemPrompt
				body["systemPrompt"] = ""
			}
			f.endpoint(t, func(r *http.Request) string {
				var request struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if len(request.Messages) < 2 || request.Messages[0].Role != "system" ||
					request.Messages[0].Content != expected+"\n\n"+forecast.MonthPlanningExplanationFormat ||
					!strings.Contains(request.Messages[1].Content, "Task instructions") {
					t.Errorf("wrong prompt roles/content: %+v", request)
				}
				plan := f.plan
				if mode == "invalid-plan" {
					plan.Entries = append([]models.Entry{}, plan.Entries...)
					plan.Entries[0].Hours++
				}
				answer, _ := json.Marshal(plan)
				return string(answer)
			})
			raw, _ := json.Marshal(body)
			before := f.store.Snapshot().Entries
			rec := monthRequest(f.server.Handler(), "/month/generate", "application/json", string(raw))
			if mode == "invalid-plan" {
				if rec.Code != http.StatusBadGateway {
					t.Fatal("custom system prompt bypassed server validation")
				}
			} else {
				if rec.Code != http.StatusOK {
					t.Fatalf("generate: %d %s", rec.Code, rec.Body.String())
				}
				if monthSystemPrompt(f.store.Snapshot()) != expected {
					t.Fatal("generation did not persist submitted configuration")
				}
				var response struct{ PreviewURL string }
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if err := f.store.Mutate(func(d *models.Data) error {
					d.Settings.MonthPlanningSystemPrompt = "Later system configuration"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				preview := httptest.NewRecorder()
				f.server.Handler().ServeHTTP(preview, httptest.NewRequest("GET", response.PreviewURL, nil))
				if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), html.EscapeString(expected)) {
					t.Fatal("preview did not retain the system prompt actually used")
				}
			}
			if !reflect.DeepEqual(before, f.store.Snapshot().Entries) {
				t.Fatal("generation modified bookings")
			}
		})
	}
}
