package models

import (
	"math/rand"
	"strings"
)

// Entry kinds distinguish forecasted (planned) from actual (booked) hours.
const (
	KindForecast = "forecast"
	KindActual   = "actual"
)

// Settings holds global configuration that is shared across all fiscal years.
// Per-fiscal-year values (target hours, vacation, standard tasks) live in
// Data.FiscalYears instead, keyed by the FY anchor year.
type Settings struct {
	Year                 int     `json:"year"`         // currently active FY anchor year
	FederalState         string  `json:"federalState"` // e.g. "BY", "BW", "BE" ...
	WeeklyTargetHours    float64 `json:"weeklyTargetHours"`
	FiscalYearStartMonth int     `json:"fiscalYearStartMonth"` // 1-12; 7 = July (default). 1 == calendar year

	// Legacy fields retained only for migrating old documents into FiscalYears.
	// They are no longer read once a per-FY entry exists. Deprecated.
	FiscalYearTargetHours float64 `json:"fiscalYearTargetHours,omitempty"`
	AnnualVacationDays    int     `json:"annualVacationDays,omitempty"`
}

// FiscalYearSettings holds configuration that changes from one fiscal year to
// the next. Vacation is entered per half-year because a fiscal year spans two
// calendar years and vacation entitlement is counted per calendar year.
type FiscalYearSettings struct {
	TargetHours       float64 `json:"targetHours"`       // total target hours for the whole FY
	VacationDaysH1    int     `json:"vacationDaysH1"`    // vacation days in the first FY half (8h each)
	VacationDaysH2    int     `json:"vacationDaysH2"`    // vacation days in the second FY half (8h each)
	StandardTaskLabel string  `json:"standardTaskLabel"` // free-text label for recurring standard tasks
	StandardTaskHours float64 `json:"standardTaskHours"` // hours deducted like holidays/vacation
}

// Project is a thing time is forecasted against, constrained by a budget.
type Project struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	BudgetHours float64 `json:"budgetHours"`
	Color       string  `json:"color"`
	Active      bool    `json:"active"`
}

// Entry is the number of hours for a project on a specific day.
// Date is stored as ISO date string (YYYY-MM-DD). Kind is either
// KindForecast (planned) or KindActual (really booked). An empty Kind
// is treated as KindForecast for backwards compatibility.
type Entry struct {
	Date      string  `json:"date"`
	ProjectID string  `json:"projectId"`
	Hours     float64 `json:"hours"`
	Kind      string  `json:"kind,omitempty"`
}

// Data is the full persisted document.
type Data struct {
	Settings    Settings                   `json:"settings"`
	FiscalYears map[int]FiscalYearSettings `json:"fiscalYears"`
	Projects    []Project                  `json:"projects"`
	Entries     []Entry                    `json:"entries"`
}

// DefaultData returns a sensible empty document for first start.
func DefaultData(year int) Data {
	return Data{
		Settings: Settings{
			Year:                 year,
			FederalState:         "BY",
			WeeklyTargetHours:    40,
			FiscalYearStartMonth: 7,
		},
		FiscalYears: map[int]FiscalYearSettings{},
		Projects:    []Project{},
		Entries:     []Entry{},
	}
}

// FYFor returns the stored settings for the given fiscal-year anchor year. When
// no per-FY entry exists yet it falls back to the legacy global fields so an
// unconfigured year starts from a sensible default.
func (d Data) FYFor(year int) FiscalYearSettings {
	if fy, ok := d.FiscalYears[year]; ok {
		return fy
	}
	return FiscalYearSettings{
		TargetHours:    d.Settings.FiscalYearTargetHours,
		VacationDaysH1: d.Settings.AnnualVacationDays,
	}
}

// CurrentFY returns the per-FY settings for the active fiscal year.
func (d Data) CurrentFY() FiscalYearSettings {
	return d.FYFor(d.Settings.Year)
}

// Palette is a set of high-contrast, visually distinct colors used to
// auto-assign a color to new projects.
var Palette = []string{
	"#e6194b", // red
	"#3cb44b", // green
	"#4363d8", // blue
	"#f58231", // orange
	"#911eb4", // purple
	"#008080", // teal
	"#f032e6", // magenta
	"#9a6324", // brown
	"#808000", // olive
	"#000075", // navy
	"#e67e22", // carrot
	"#2ecc71", // emerald
	"#c0392b", // pomegranate
	"#1abc9c", // turquoise
	"#34495e", // slate
	"#d35400", // pumpkin
	"#16a085", // green sea
	"#8e44ad", // wisteria
	"#2980b9", // strong blue
	"#27ae60", // nephritis
}

// RandomColor returns a random color from the palette, preferring colors not
// already present in used. If every palette color is taken, it falls back to a
// random palette color.
func RandomColor(used []string) string {
	taken := make(map[string]bool, len(used))
	for _, c := range used {
		taken[strings.ToLower(strings.TrimSpace(c))] = true
	}
	avail := make([]string, 0, len(Palette))
	for _, c := range Palette {
		if !taken[strings.ToLower(c)] {
			avail = append(avail, c)
		}
	}
	if len(avail) == 0 {
		avail = Palette
	}
	return avail[rand.Intn(len(avail))]
}
