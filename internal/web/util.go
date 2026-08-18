package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

// errAssignmentTaken aborts a write that would put the same assignment on two
// project rows of one fiscal year. Their hours are pooled, so a second row
// would show the same figures twice.
var errAssignmentTaken = errors.New("assignment id already used in this fiscal year")

// assignmentTaken reports whether another project of the given fiscal year
// already carries this assignment ID. exceptID is the row being edited.
func assignmentTaken(ps []models.Project, assignmentID string, year int, exceptID string) bool {
	want := strings.ToLower(strings.TrimSpace(assignmentID))
	if want == "" {
		return false
	}
	for _, p := range ps {
		if p.ID == exceptID || p.FiscalYear != year {
			continue
		}
		if strings.ToLower(strings.TrimSpace(p.AssignmentID)) == want {
			return true
		}
	}
	return false
}

// isAutoSave reports whether the request comes from the background auto-save
// (a fetch call) rather than a normal form submit. Those callers stay on the
// page, so the handler answers with a bare status code instead of a redirect.
func isAutoSave(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Requested-With"), "fetch")
}

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
