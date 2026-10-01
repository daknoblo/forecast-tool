package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestCatalogCoverageAndPlaceholders(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(string(englishJSON)))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	format := regexp.MustCompile(`%[0-9.]*[sdgfqvw%]`)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		message := key.(string)
		if seen[message] {
			t.Errorf("duplicate message %q", message)
		}
		seen[message] = true
		var translation string
		if err := decoder.Decode(&translation); err != nil {
			t.Fatal(err)
		}
		if translation == "" {
			t.Errorf("empty translation for %q", message)
		}
		if !reflect.DeepEqual(format.FindAllString(message, -1), format.FindAllString(translation, -1)) {
			t.Errorf("translation changes format placeholders: %q -> %q", message, translation)
		}
	}
	files, err := filepath.Glob("../web/templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("template discovery: %v", err)
	}
	call := regexp.MustCompile(`{{t ("(?:\\.|[^"\\])*")}}`)
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range call.FindAllStringSubmatch(string(source), -1) {
			message, err := strconv.Unquote(match[1])
			if err != nil {
				t.Fatal(err)
			}
			if !seen[message] {
				t.Errorf("%s: missing translation for %q", file, message)
			}
		}
	}
}

func TestTranslationPreservesInterpolatedContent(t *testing.T) {
	message := "Der %s darf höchstens %d Zeichen enthalten."
	value := "Urlaub <script>alert('x')</script>"
	translated := Text(English, fmt.Sprintf(message, value, 8000))
	want := fmt.Sprintf(Text(English, message), value, 8000)
	if translated != want || !strings.Contains(translated, value) {
		t.Fatalf("interpolated content changed: %q; want %q", translated, want)
	}
	if Text(German, "Einstellungen") != "Einstellungen" || Text(English, "Einstellungen") != "Settings" {
		t.Fatal("basic translation failed")
	}
	if Language("") != German || Valid("fr") {
		t.Fatal("language defaults or validation changed")
	}
	if got := Text(English, "noch 9 Monate"); got != fmt.Sprintf(Text(English, "noch %d Monate"), 9) {
		t.Fatalf("numeric placeholder matched a sentence prefix: %q", got)
	}
}

func TestChartTranslationPrecedesInterpolation(t *testing.T) {
	tr := Translator(English)
	source := `<svg aria-label="Auslastung"><text>Gebucht</text><title>%s</title></svg>`
	result := fmt.Sprintf(tr(source), "Urlaub")
	if !strings.Contains(result, `aria-label="Utilization"`) || !strings.Contains(result, ">Booked<") {
		t.Fatalf("chart labels not translated: %s", result)
	}
	if !strings.Contains(result, "<title>Urlaub</title>") {
		t.Fatal("user project name was translated")
	}
}
