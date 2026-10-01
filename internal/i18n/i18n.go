package i18n

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	German  = "de"
	English = "en"
)

//go:embed en.json
var englishJSON []byte

var english = func() map[string]string {
	var messages map[string]string
	if err := json.Unmarshal(englishJSON, &messages); err != nil {
		panic(fmt.Sprintf("invalid English message catalog: %v", err))
	}
	return messages
}()

type messagePattern struct {
	match *regexp.Regexp
	parts []string
}

var placeholders = regexp.MustCompile(`%[0-9.]*[sdgfqvw]`)

var patterns = func() []messagePattern {
	keys := make([]string, 0, len(english))
	for key := range english {
		if placeholders.MatchString(key) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		left := len(placeholders.ReplaceAllString(keys[i], ""))
		right := len(placeholders.ReplaceAllString(keys[j], ""))
		if left != right {
			return left > right
		}
		return keys[i] < keys[j]
	})
	result := make([]messagePattern, 0, len(keys))
	for _, key := range keys {
		pieces := placeholders.Split(key, -1)
		formats := placeholders.FindAllString(key, -1)
		var expression strings.Builder
		expression.WriteString("(?s)^")
		for index, piece := range pieces {
			if index > 0 {
				format := formats[index-1]
				switch format[len(format)-1] {
				case 'd':
					expression.WriteString(`(-?\d+)`)
				case 'g', 'f':
					expression.WriteString(`([-+]?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?)`)
				default:
					expression.WriteString("(.*?)")
				}
			}
			expression.WriteString(regexp.QuoteMeta(strings.ReplaceAll(piece, "%%", "%")))
		}
		expression.WriteString("$")
		translated := placeholders.Split(strings.ReplaceAll(english[key], "%%", "%"), -1)
		if len(translated) != len(pieces) {
			panic(fmt.Sprintf("translation changes placeholders: %s", key))
		}
		result = append(result, messagePattern{regexp.MustCompile(expression.String()), translated})
	}
	return result
}()

func Valid(language string) bool {
	return language == German || language == English
}

// Language preserves the German default of documents created before localization.
func Language(language string) string {
	if language == English {
		return English
	}
	return German
}

// Text translates application-owned messages, never user-entered content.
// Formatted domain messages are matched as a whole, preserving interpolated data.
func Text(language, message string) string {
	if language == English {
		if translated, ok := english[message]; ok {
			return translated
		}
		if core := strings.TrimSpace(message); core != message {
			if translated, ok := english[core]; ok {
				start := strings.Index(message, core)
				return message[:start] + translated + message[start+len(core):]
			}
		}
		for _, pattern := range patterns {
			if values := pattern.match.FindStringSubmatch(message); values != nil {
				var result strings.Builder
				for index, part := range pattern.parts {
					if index > 0 {
						result.WriteString(values[index])
					}
					result.WriteString(part)
				}
				return result.String()
			}
		}
	}
	return message
}

func Format(language, message string, args ...any) string {
	return fmt.Sprintf(Text(language, message), args...)
}

func Date(language string, date time.Time) string {
	if language == English {
		return date.Format("2006-01-02")
	}
	return date.Format("02.01.2006")
}

func ShortDate(language string, date time.Time) string {
	if language == English {
		return date.Format("02 Jan")
	}
	return date.Format("02.01.")
}

var markupText = regexp.MustCompile(`>[^<>]+<`)
var markupLabel = regexp.MustCompile(`aria-label="[^"]+"`)

// Translator binds one language to application-owned chart format strings.
// Translation happens before interpolation, so project names remain untouched.
func Translator(languages ...string) func(string) string {
	language := German
	if len(languages) > 0 {
		language = Language(languages[0])
	}
	return func(message string) string {
		if language == German {
			return message
		}
		if !strings.Contains(message, "<") {
			return Text(language, message)
		}
		message = markupText.ReplaceAllStringFunc(message, func(part string) string {
			return ">" + Text(language, part[1:len(part)-1]) + "<"
		})
		return markupLabel.ReplaceAllStringFunc(message, func(part string) string {
			return `aria-label="` + Text(language, part[12:len(part)-1]) + `"`
		})
	}
}
