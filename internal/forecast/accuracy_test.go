package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestForecastAccuracy(t *testing.T) {
	cases := []struct {
		name, asOf, now               string
		year, month, evaluated, total int
		percentage                    float64
		stale                         bool
	}{
		{"first partial week", "2026-07-01", "2026-07-01", 2027, 7, 0, 53, 100, false},
		{"Sunday not finished", "2026-07-05", "2026-07-05", 2027, 7, 0, 53, 100, false},
		{"first Monday", "2026-07-06", "2026-07-06", 2027, 7, 1, 53, 100, false},
		{"twelve complete", "2026-09-24", "2026-09-24", 2027, 7, 12, 53, 100, false},
		{"rounded source", "2026-09-24", "2026-09-24", 2027, 7, 12, 53, 91.67, false},
		{"zero valid", "2026-09-24", "2026-09-24", 2027, 7, 12, 53, 0, false},
		{"stale stays anchored", "2026-09-24", "2026-10-05", 2027, 7, 12, 53, 100, true},
		{"same week fresh", "2026-09-21", "2026-09-27", 2027, 7, 12, 53, 100, false},
		{"last FY day", "2027-06-30", "2027-06-30", 2027, 7, 52, 53, 90, false},
		{"after partial FY end", "2027-07-01", "2027-07-01", 2027, 7, 53, 53, 90, false},
		{"January", "2026-01-05", "2026-01-05", 2026, 1, 1, 53, 100, false},
		{"54 week leap FY", "2012-12-31", "2012-12-31", 2012, 1, 53, 54, 100, false},
		{"54 complete", "2013-01-01", "2013-01-01", 2012, 1, 54, 54, 100, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := models.DefaultData(tc.year)
			d.Settings.FiscalYearStartMonth = tc.month
			d.ForecastAccuracy = map[int]models.ForecastAccuracy{
				tc.year: {Percentage: tc.percentage, AsOf: tc.asOf, FiscalYearStartMonth: tc.month},
			}
			now, _ := time.Parse("2006-01-02", tc.now)
			got := BuildForecastAccuracy(d, now)
			if got.Error != "" || !got.HasData || got.HasProjection != (tc.evaluated > 0) ||
				got.EvaluatedWeeks != tc.evaluated || got.TotalWeeks != tc.total ||
				got.RemainingWeeks != tc.total-tc.evaluated || got.Stale != tc.stale || got.Percentage != tc.percentage {
				t.Fatalf("unexpected summary: %+v", got)
			}
			if tc.evaluated > 0 {
				minimum := tc.percentage * float64(tc.evaluated) / float64(tc.total)
				if math.Abs(got.MinimumPct-minimum) > 1e-10 || math.Abs(got.DrawdownPoints-(tc.percentage-minimum)) > 1e-10 {
					t.Fatalf("projection: %+v, want minimum %f", got, minimum)
				}
			}
		})
	}
}

func TestForecastAccuracyMissingInvalidAndChangedCalendar(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	d := models.DefaultData(2027)
	if got := BuildForecastAccuracy(d, now); got.HasData || got.HasProjection {
		t.Fatalf("missing metric fabricated: %+v", got)
	}
	d.ForecastAccuracy = map[int]models.ForecastAccuracy{2026: {Percentage: 80, AsOf: "2026-09-24", FiscalYearStartMonth: 7}}
	if got := BuildForecastAccuracy(d, now); got.HasData {
		t.Fatalf("another FY leaked: %+v", got)
	}
	for _, value := range []models.ForecastAccuracy{
		{Percentage: -1, AsOf: "2026-09-24", FiscalYearStartMonth: 7},
		{Percentage: 101, AsOf: "2026-09-24", FiscalYearStartMonth: 7},
		{Percentage: math.NaN(), AsOf: "2026-09-24", FiscalYearStartMonth: 7},
		{Percentage: math.Inf(1), AsOf: "2026-09-24", FiscalYearStartMonth: 7},
		{Percentage: 100, AsOf: "2026-02-30", FiscalYearStartMonth: 7},
		{Percentage: 100, AsOf: "2026-07-1", FiscalYearStartMonth: 7},
		{Percentage: 100, AsOf: "2026-09-25", FiscalYearStartMonth: 7},
		{Percentage: 100, AsOf: "2026-06-30", FiscalYearStartMonth: 7},
		{Percentage: 100, AsOf: "2026-09-24", FiscalYearStartMonth: 13},
	} {
		d.ForecastAccuracy[2027] = value
		if got := BuildForecastAccuracy(d, now); got.Error == "" || got.HasProjection {
			t.Fatalf("invalid metric accepted: %+v -> %+v", value, got)
		}
	}
	d.ForecastAccuracy[2027] = models.ForecastAccuracy{Percentage: 100, AsOf: "2026-09-24", FiscalYearStartMonth: 7}
	d.Settings.FiscalYearStartMonth = 8
	if got := BuildForecastAccuracy(d, now); !got.HasData || got.HasProjection || got.Error == "" {
		t.Fatalf("changed FY calendar not surfaced: %+v", got)
	}
}
