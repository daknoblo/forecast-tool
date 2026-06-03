package web

import (
	"strconv"
	"strings"
)

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
