package web

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMonthModelResponseDisplay(t *testing.T) {
	f := newMonthAIFixture(t)
	f.plan.Explanation = "MODEL-ONLY </textarea><script>alert(1)</script>"
	raw, err := json.Marshal(f.plan)
	if err != nil {
		t.Fatal(err)
	}
	answer := strings.ReplaceAll(strings.ReplaceAll(string(raw), `\u003c`, "<"), `\u003e`, ">")
	f.endpoint(t, func(*http.Request) string { return answer })
	location, token := f.generate(t)
	rec := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", location, nil))
	field := regexp.MustCompile(`(?s)<textarea id="month-response"([^>]*)>(.*?)</textarea>`).FindStringSubmatch(rec.Body.String())
	var expected bytes.Buffer
	if err := json.Indent(&expected, []byte(answer), "", "  "); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(field) != 3 ||
		!strings.Contains(field[1], "readonly") || strings.Contains(field[1], "name=") ||
		html.UnescapeString(field[2]) != expected.String() {
		t.Fatal("model answer must be fully preserved, indented and read-only")
	}
	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("model response was injected as HTML")
	}
	promptForm := regexp.MustCompile(`(?s)<form[^>]*action="/month/prompt".*?</form>`).FindString(rec.Body.String())
	if strings.Contains(promptForm, `id="month-response"`) {
		t.Fatal("model response must not be submitted as prompt configuration")
	}
	data, err := os.ReadFile(f.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "MODEL-ONLY") {
		t.Fatal("model diagnostics must not be persisted in application data")
	}
	for _, mode := range []string{"private", "expired"} {
		req := httptest.NewRequest("GET", location, nil)
		if mode == "private" {
			req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
		} else {
			preview := f.server.monthAI.previews[token]
			preview.Created = time.Now().Add(-monthPreviewTTL - time.Minute)
			f.server.monthAI.previews[token] = preview
		}
		rec = httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "MODEL-ONLY") {
			t.Fatalf("%s page exposed model response", mode)
		}
	}
}

func TestMonthRejectedModelResponse(t *testing.T) {
	for _, answer := range []string{
		`{"entries":[],"unallocated":[],"explanation":"MODEL-ONLY invalid totals"}`,
		`MODEL-ONLY invalid JSON </textarea><script>alert(1)</script>`,
	} {
		f := newMonthAIFixture(t)
		f.endpoint(t, func(*http.Request) string { return answer })
		rec := monthRequest(f.server.Handler(), "/month/generate", "application/json", `{"month":"`+f.month+`","prompt":""}`)
		var response struct {
			Error         string `json:"error"`
			ModelResponse string `json:"modelResponse"`
			PreviewURL    string `json:"previewURL"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadGateway || response.Error == "" ||
			response.ModelResponse != answer || response.PreviewURL != "" || len(f.server.monthAI.previews) != 0 {
			t.Fatal("rejected output must remain inspectable without becoming a saveable preview")
		}
	}
}
