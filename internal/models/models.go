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

// Settings holds global configuration for a single fiscal year.
type Settings struct {
	Year                  int     `json:"year"`
	FederalState          string  `json:"federalState"` // e.g. "BY", "BW", "BE" ...
	WeeklyTargetHours     float64 `json:"weeklyTargetHours"`
	FiscalYearTargetHours float64 `json:"fiscalYearTargetHours"` // total target hours for the whole FY
	FiscalYearStartMonth  int     `json:"fiscalYearStartMonth"`  // 1-12; 7 = July (default). 1 == calendar year
	AnnualVacationDays    int     `json:"annualVacationDays"`    // planned vacation days per FY (8h each)
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
	Settings Settings  `json:"settings"`
	Projects []Project `json:"projects"`
	Entries  []Entry   `json:"entries"`
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
		Projects: []Project{},
		Entries:  []Entry{},
	}
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
