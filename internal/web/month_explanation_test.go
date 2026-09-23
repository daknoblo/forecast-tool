package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMonthExplanationFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{
			"projects",
			"Nur eine Vorschau.\n\n- **Projekt Alpha**: Vier Tage mit je 2 h.\n- **Projekt Beta**: *Unsicheres* Muster.\n\nAllgemeine Hinweise.",
			[]string{"<p>Nur eine Vorschau.</p>", "<ul>", "<li><strong>Projekt Alpha</strong>", "<li><strong>Projekt Beta</strong>", "<em>Unsicheres</em>", "<p>Allgemeine Hinweise.</p>"},
		},
		{
			"plain text and line breaks", "Vorhandener Text & Stunden.\nWeitere Zeile.\n\nNeuer Absatz.",
			[]string{"Vorhandener Text &amp; Stunden.<br>", "Weitere Zeile.", "<p>Neuer Absatz.</p>"},
		},
		{
			"numbered and nested lists", "1. **Alpha**\n   - Zwei Stunden\n2. Beta",
			[]string{"<ol>", "<strong>Alpha</strong>", "<ul>", "<li>Zwei Stunden</li>", "<li>Beta</li>"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rendered, err := renderMonthExplanation(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(rendered), want) {
					t.Errorf("missing %q in %s", want, rendered)
				}
			}
		})
	}
}

func TestMonthExplanationUntrustedMarkup(t *testing.T) {
	input := `<script>alert(1)</script>

<iframe src="/export"></iframe>

<style>body {display:none}</style>

<img src="/export" onerror="alert(1)">

- **Alpha** <svg onload="alert(1)"></svg>
- [Projekt](javascript:alert%281%29)
- [Externer Link](https://example.invalid/private)
- ![Bildbeschreibung](https://example.invalid/track)
- <https://example.invalid/?x=1&y=2>
- <test@example.invalid>
- ` + "`<script>sichtbarer Code</script>`"
	rendered, err := renderMonthExplanation(input)
	if err != nil {
		t.Fatal(err)
	}
	output := string(rendered)
	for _, forbidden := range []string{"<script", "<iframe", "<style", "<img", "<svg", "<a ", "href=", "src=", "onerror=", "onload="} {
		if strings.Contains(output, forbidden) {
			t.Errorf("unsafe markup %q in %s", forbidden, output)
		}
	}
	for _, want := range []string{"<strong>Alpha</strong>", "Projekt", "Externer Link", "Bildbeschreibung", "?x=1&amp;y=2", "test@example.invalid", "&lt;script&gt;sichtbarer Code&lt;/script&gt;"} {
		if !strings.Contains(output, want) {
			t.Errorf("lost safe text %q in %s", want, output)
		}
	}
}

func TestMonthPreviewRendersExplanationMarkdown(t *testing.T) {
	f := newMonthAIFixture(t)
	f.plan.Explanation = "Vorschau.\n\n- **Projekt Pattern**: Vier Blöcke mit je zwei Stunden.\n\nUrlaub bleibt unverändert."
	f.endpoint(t, func(*http.Request) string {
		answer, _ := json.Marshal(f.plan)
		return string(answer)
	})
	location, _ := f.generate(t)
	rec := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", location, nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<div class="month-explanation"><p>Vorschau.</p>`) ||
		!strings.Contains(body, "<li><strong>Projekt Pattern</strong>: Vier Blöcke mit je zwei Stunden.</li>") ||
		strings.Contains(body, "**Projekt Pattern**") {
		t.Fatalf("preview explanation was not rendered as Markdown: %d %s", rec.Code, body)
	}
}
