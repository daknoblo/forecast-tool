package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
)

// newID returns a compact, time-ordered identifier for a new project, matching
// the format used by the web UI (base-36 nanosecond timestamp).
func newID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// capLen truncates s to at most n runes so client-supplied strings cannot grow
// the stored document without bound.
func capLen(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}

// optionalISO validates an optional YYYY-MM-DD date. An empty string is valid
// and yields "". A malformed date yields an error.
func optionalISO(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", err
	}
	return s, nil
}

// validState reports whether code is one of the supported German federal states.
func validState(code string) bool {
	for _, st := range holidays.States {
		if st.Code == code {
			return true
		}
	}
	return false
}
