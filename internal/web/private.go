package web

import (
	"net/http"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/sample"
)

// privateCookie holds the private ("presentation") mode flag. The mode only
// changes how the UI is rendered, so it lives in a per-browser cookie instead of
// the shared data document.
const privateCookie = "forecast_private"

// privateMaxAge keeps the preference for a year (in seconds).
const privateMaxAge = 365 * 24 * 60 * 60

// isPrivate reports whether the private mode is enabled for this request.
func isPrivate(r *http.Request) bool {
	c, err := r.Cookie(privateCookie)
	return err == nil && c.Value == "1"
}

// handlePrivateToggle flips the private mode and returns to the page the user
// came from.
func (s *Server) handlePrivateToggle(w http.ResponseWriter, r *http.Request) {
	c := &http.Cookie{
		Name:     privateCookie,
		Value:    "1",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   privateMaxAge,
	}
	if isPrivate(r) { // currently on -> turn it off
		c.Value = ""
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
	http.Redirect(w, r, refererPath(r), http.StatusSeeOther) // #nosec G710 -- same-origin path/query only
}

// viewData returns the document a page renders: the real one, or - while the
// private mode is on - a fictional sample document with the same settings.
// Replacing the figures instead of blanking them out keeps every bar, chart and
// indicator alive, so the page still reads like the real thing on a shared
// screen.
//
// Only read paths use it; every write keeps going through the store, so a
// sample project can never be persisted.
func (s *Server) viewData(r *http.Request) models.Data {
	d := s.store.Snapshot()
	if !isPrivate(r) {
		return d
	}
	return sample.Data(time.Now(), d)
}

// barWidth clamps a percentage into [0, 100] for use as a CSS width.
func barWidth(pct float64) string {
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return formatHours(pct)
}
