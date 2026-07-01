package web

import (
	"strconv"
	"strings"
	"time"
)

// capLen truncates s to at most n runes, bounding how long user-supplied
// strings (names, labels) can grow in the stored document and rendered UI.
func capLen(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}

// validISODate returns s when it is a valid YYYY-MM-DD date, otherwise "". It
// is used to reject malformed dates from crafted form posts (the date picker
// itself only ever submits ISO dates or an empty string).
func validISODate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

// formatHours renders a float without trailing ".0" for whole numbers.
func formatHours(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// normalizeNum accepts both comma and dot as decimal separators.
func normalizeNum(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return "0"
	}
	return s
}

func trim(s string) string { return strings.TrimSpace(s) }
