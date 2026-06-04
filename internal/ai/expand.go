package ai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

// maxExpandedEntries caps how many entries a forecast plan may generate, so a
// careless directive cannot blow up the document.
const maxExpandedEntries = 8000

// forecastDirective is a compact instruction emitted by the model instead of
// hundreds of per-day entries. The server expands it into Monday–Friday entries
// across the whole fiscal year, which keeps the model's output small enough to
// avoid token-limit truncation.
type forecastDirective struct {
	ProjectID    string  `json:"projectId"`
	FiscalYear   int     `json:"fiscalYear"`
	HoursPerWeek float64 `json:"hoursPerWeek"`
	Kind         string  `json:"kind"`
}

// ExpandPlan looks for an optional "forecastPlan" array in the model's JSON
// output and expands each directive into evenly distributed weekday (Mo–Fr)
// entries for the referenced fiscal year. The resulting document no longer
// contains "forecastPlan" and is therefore valid against the strict schema.
// If no forecastPlan is present, the input is returned unchanged.
func ExpandPlan(raw []byte, defaultStartMonth int) ([]byte, error) {
	// The plan lives alongside the normal document fields; pull it out first.
	var wrap struct {
		ForecastPlan []forecastDirective `json:"forecastPlan"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		// Not parseable as an object with a plan – leave it to downstream
		// validation to produce a helpful error.
		return raw, nil
	}
	if len(wrap.ForecastPlan) == 0 {
		return raw, nil
	}

	// Unknown fields (like forecastPlan) are ignored by the standard decoder, so
	// this yields the document without the directive.
	var data models.Data
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("KI-Antwort konnte nicht expandiert werden: %w", err)
	}

	startMonth := defaultStartMonth
	if data.Settings.FiscalYearStartMonth != 0 {
		startMonth = data.Settings.FiscalYearStartMonth
	}

	// Track existing entries to avoid creating duplicates for the same
	// day/project/kind combination.
	seen := make(map[string]bool, len(data.Entries))
	for _, e := range data.Entries {
		seen[entryKey(e.Date, e.ProjectID, e.Kind)] = true
	}

	for _, d := range wrap.ForecastPlan {
		if d.ProjectID == "" || d.FiscalYear == 0 || d.HoursPerWeek <= 0 {
			continue
		}
		kind := d.Kind
		if kind == "" {
			kind = models.KindForecast
		}
		perDay := d.HoursPerWeek / 5
		start, end := forecast.FiscalYear(d.FiscalYear, startMonth)
		for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
			if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
				continue
			}
			date := day.Format("2006-01-02")
			key := entryKey(date, d.ProjectID, kind)
			if seen[key] {
				continue
			}
			seen[key] = true
			data.Entries = append(data.Entries, models.Entry{
				Date:      date,
				ProjectID: d.ProjectID,
				Hours:     perDay,
				Kind:      kind,
			})
			if len(data.Entries) > maxExpandedEntries {
				return nil, fmt.Errorf("forecastPlan würde zu viele Einträge erzeugen (>%d) – bitte Zeitraum/Projekte eingrenzen", maxExpandedEntries)
			}
		}
	}

	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("expandiertes Dokument konnte nicht serialisiert werden: %w", err)
	}
	return out, nil
}

func entryKey(date, projectID, kind string) string {
	if kind == "" {
		kind = models.KindForecast
	}
	return date + "|" + projectID + "|" + kind
}
