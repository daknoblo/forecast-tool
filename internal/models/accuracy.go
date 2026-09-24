package models

import (
	"fmt"
	"math"
	"time"
)

// ForecastAccuracy is an imported ESXP percentage, not a locally reconstructed
// comparison of entries. The calendar is captured to preserve its denominator.
type ForecastAccuracy struct {
	Percentage           float64 `json:"percentage"`
	AsOf                 string  `json:"asOf"`
	FiscalYearStartMonth int     `json:"fiscalYearStartMonth"`
}

func (a ForecastAccuracy) Validate() error {
	if math.IsNaN(a.Percentage) || math.IsInf(a.Percentage, 0) || a.Percentage < 0 || a.Percentage > 100 {
		return fmt.Errorf("Forecast Accuracy muss zwischen 0 und 100 Prozent liegen")
	}
	if date, err := time.Parse("2006-01-02", a.AsOf); err != nil || date.Format("2006-01-02") != a.AsOf {
		return fmt.Errorf("asOf muss ein gültiges Datum (YYYY-MM-DD) sein")
	}
	if a.FiscalYearStartMonth < 1 || a.FiscalYearStartMonth > 12 {
		return fmt.Errorf("Der FY-Startmonat der Forecast Accuracy muss zwischen 1 und 12 liegen")
	}
	return nil
}
