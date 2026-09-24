package forecast

import (
	"fmt"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

type AccuracySummary struct {
	FiscalYear     int     `json:"fiscalYear"`
	HasData        bool    `json:"hasData"`
	HasProjection  bool    `json:"hasProjection"`
	Percentage     float64 `json:"percentage"`
	MinimumPct     float64 `json:"minimumPct"`
	DrawdownPoints float64 `json:"drawdownPoints"`
	AsOf           string  `json:"asOf,omitempty"`
	AsOfLabel      string  `json:"-"`
	EvaluatedWeeks int     `json:"evaluatedWeeks"`
	RemainingWeeks int     `json:"remainingWeeks"`
	TotalWeeks     int     `json:"totalWeeks"`
	Stale          bool    `json:"stale"`
	Error          string  `json:"error,omitempty"`
}

func ValidateForecastAccuracy(year int, value models.ForecastAccuracy, now time.Time) error {
	if !models.ValidYear(year) {
		return fmt.Errorf("Ungültiges Fiskaljahr der Forecast Accuracy")
	}
	if err := value.Validate(); err != nil {
		return err
	}
	start, _ := FiscalYear(year, value.FiscalYearStartMonth)
	if value.AsOf < start.Format("2006-01-02") {
		return fmt.Errorf("asOf darf nicht vor Beginn des Fiskaljahres liegen")
	}
	if value.AsOf > now.UTC().Format("2006-01-02") {
		return fmt.Errorf("asOf darf nicht in der Zukunft liegen")
	}
	return nil
}

// completedAccuracyWeeks excludes the current Monday-based week. At FY end,
// the final partial week is complete as soon as its last in-FY day has passed.
func completedAccuracyWeeks(year, startMonth int, asOf time.Time) int {
	_, end := FiscalYear(year, startMonth)
	completed := 0
	for week := 1; week <= FYWeeks(year, startMonth); week++ {
		last := FYWeekMonday(year, startMonth, week).AddDate(0, 0, 6)
		if last.After(end) {
			last = end
		}
		if last.Format("2006-01-02") < asOf.UTC().Format("2006-01-02") {
			completed++
		}
	}
	return completed
}

func BuildForecastAccuracy(d models.Data, now time.Time) AccuracySummary {
	year, startMonth := d.Settings.Year, normMonth(d.Settings.FiscalYearStartMonth)
	result := AccuracySummary{FiscalYear: year, TotalWeeks: FYWeeks(year, startMonth)}
	value, ok := d.ForecastAccuracy[year]
	if !ok {
		return result
	}
	if err := ValidateForecastAccuracy(year, value, now); err != nil {
		result.Error = err.Error()
		return result
	}
	result.HasData, result.Percentage, result.AsOf = true, value.Percentage, value.AsOf
	asOf, _ := time.Parse("2006-01-02", value.AsOf)
	result.AsOfLabel = asOf.Format("02.01.2006")
	if value.FiscalYearStartMonth != startMonth {
		result.Error = "Der FY-Startmonat wurde geändert. Forecast Accuracy bitte erneut aus ESXP übertragen."
		return result
	}
	result.EvaluatedWeeks = completedAccuracyWeeks(year, startMonth, asOf)
	result.RemainingWeeks = result.TotalWeeks - result.EvaluatedWeeks
	result.Stale = completedAccuracyWeeks(year, startMonth, now) > result.EvaluatedWeeks
	if result.EvaluatedWeeks == 0 {
		return result
	}
	result.HasProjection = true
	// Do not round inferred successful weeks: ESXP may report a rounded rate.
	result.MinimumPct = value.Percentage * float64(result.EvaluatedWeeks) / float64(result.TotalWeeks)
	result.DrawdownPoints = value.Percentage - result.MinimumPct
	return result
}
