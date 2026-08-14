package web

import (
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"

	"github.com/daknoblo/forecast-tool/internal/models"
)

// privateCookie holds the private ("presentation") mode flag. The mode only
// changes how the UI is rendered, so it lives in a per-browser cookie instead of
// the shared data document.
const privateCookie = "forecast_private"

// maskedValue replaces every figure while the private mode is on.
const maskedValue = "•••"

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

// privacyFuncs returns the render-time template functions whose output depends
// on the private mode. They are layered over the base function map for a single
// request, so masking applies to every template without touching each call site.
func privacyFuncs(private bool) template.FuncMap {
	if !private {
		return template.FuncMap{
			"hours":    formatHours,
			"pct":      func(f float64) string { return formatHours(f) + " %" },
			"barWidth": barWidth,
		}
	}
	return template.FuncMap{
		"hours":    func(float64) string { return maskedValue },
		"pct":      func(float64) string { return maskedValue },
		"barWidth": func(float64) string { return "0" },
	}
}

// maskProjects replaces project names with neutral placeholders and drops the
// assignment IDs so a shared screen never reveals customer or project names.
// The mapping is stable (ordered by project ID), so a project keeps the same
// placeholder across pages and reloads.
func maskProjects(d models.Data) models.Data {
	ids := make([]string, 0, len(d.Projects))
	for _, p := range d.Projects {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	alias := make(map[string]string, len(ids))
	for i, id := range ids {
		alias[id] = "Projekt " + projectAlias(i)
	}
	out := make([]models.Project, len(d.Projects))
	copy(out, d.Projects)
	for i := range out {
		out[i].Name = alias[out[i].ID]
		out[i].AssignmentID = ""
	}
	d.Projects = out
	return d
}

// projectAlias returns A..Z, then AA, AB, ... for short, stable placeholders.
func projectAlias(i int) string {
	out := ""
	for {
		out = string(rune('A'+i%26)) + out
		i = i/26 - 1
		if i < 0 {
			return out
		}
	}
}

// maskIfPrivate applies maskProjects when the request runs in private mode.
func maskIfPrivate(d models.Data, r *http.Request) models.Data {
	if isPrivate(r) {
		return maskProjects(d)
	}
	return d
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

// chartHours formats a value for an inline SVG chart, masking it in private mode.
func chartHours(v float64, private bool) string {
	if private {
		return maskedValue
	}
	return formatHours(v)
}

// chartPct formats a whole percentage for an inline SVG chart axis, masking it
// in private mode.
func chartPct(v float64, private bool) string {
	if private {
		return maskedValue
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64) + " %"
}
