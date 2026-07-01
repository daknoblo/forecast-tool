package models

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Entry kinds distinguish forecasted (planned) from actual (booked) hours.
const (
	KindForecast = "forecast"
	KindActual   = "actual"
)

// MinYear and MaxYear bound any fiscal-year anchor accepted from user input.
const (
	MinYear = 2000
	MaxYear = 2100
)

// ValidYear reports whether y is a plausible fiscal-year anchor year.
func ValidYear(y int) bool {
	return y >= MinYear && y <= MaxYear
}

// Settings holds global configuration that is shared across all fiscal years.
// Per-fiscal-year values (target hours, vacation, standard tasks) live in
// Data.FiscalYears instead, keyed by the FY anchor year.
type Settings struct {
	Year                 int     `json:"year"`         // currently active FY anchor year
	FederalState         string  `json:"federalState"` // e.g. "BY", "BW", "BE" ...
	WeeklyTargetHours    float64 `json:"weeklyTargetHours"`
	FiscalYearStartMonth int     `json:"fiscalYearStartMonth"` // 1-12; 7 = July (default). 1 == calendar year

	// AI holds the configuration for the remote AI endpoint used to update the
	// JSON document from a natural-language prompt.
	AI AISettings `json:"ai"`

	// Utilization configures the booking traffic-light thresholds and labels
	// (global, shared across all fiscal years).
	Utilization UtilizationSettings `json:"utilization"`

	// Legacy fields retained only for migrating old documents into FiscalYears.
	// They are no longer read once a per-FY entry exists. Deprecated.
	FiscalYearTargetHours float64 `json:"fiscalYearTargetHours,omitempty"`
	AnnualVacationDays    int     `json:"annualVacationDays,omitempty"`
}

// AISettings configures the remote AI endpoint (e.g. an Azure AI Foundry
// model-router exposing an Azure OpenAI-compatible chat-completions API).
// The secret API key is provided via the FORECAST_AI_API_KEY environment
// variable, not stored here. APIKey is retained only for backward-compatible
// reading of old documents and is no longer written.
type AISettings struct {
	Endpoint   string `json:"endpoint"`         // base URL, e.g. https://my-resource.openai.azure.com
	APIKey     string `json:"apiKey,omitempty"` // deprecated: prefer FORECAST_AI_API_KEY env var
	Deployment string `json:"deployment"`       // deployment / model-router name
	APIVersion string `json:"apiVersion"`       // e.g. 2024-10-21
}

// UtilizationSettings configures the booking traffic-light ("Ampel"). The four
// tiers are derived from three weekly-hour thresholds; each tier carries a
// freely editable label shown in the week/overview tables.
//
//	hours <= MinHours              -> "min"     (blue, arrow down)
//	MinHours < hours <= OptimalHours -> "optimal" (green, OK)
//	OptimalHours < hours < OverHours -> "high"    (orange, arrow up)
//	hours >= OverHours             -> "over"    (red, X)
type UtilizationSettings struct {
	MinHours     float64 `json:"minHours"`     // lower bound (Burnrate Minimum)
	OptimalHours float64 `json:"optimalHours"` // optimal weekly hours
	OverHours    float64 `json:"overHours"`    // overbooked threshold
	MinLabel     string  `json:"minLabel"`
	OptimalLabel string  `json:"optimalLabel"`
	HighLabel    string  `json:"highLabel"`
	OverLabel    string  `json:"overLabel"`
}

// DefaultUtilization returns the standard traffic-light thresholds and labels.
func DefaultUtilization() UtilizationSettings {
	return UtilizationSettings{
		MinHours:     26,
		OptimalHours: 40,
		OverHours:    60,
		MinLabel:     "Burnrate Minimum",
		OptimalLabel: "Optimal",
		HighLabel:    "Zu hoch",
		OverLabel:    "Überbucht",
	}
}

// UtilStatus is the classified booking status for a number of weekly hours.
// Key is one of "min", "optimal", "high", "over" and drives the dot's color and
// symbol in the templates.
type UtilStatus struct {
	Key   string  // min | optimal | high | over
	Label string  // user-defined label
	Hours float64 // the hours that were classified
}

// ClassifyUtilization maps weekly booked hours to a traffic-light status using
// the configured thresholds, falling back to the defaults when unset.
func (s Settings) ClassifyUtilization(hours float64) UtilStatus {
	u := s.Utilization
	if u.MinHours == 0 && u.OptimalHours == 0 && u.OverHours == 0 {
		u = DefaultUtilization()
	}
	switch {
	case hours <= u.MinHours:
		return UtilStatus{Key: "min", Label: labelOr(u.MinLabel, "Burnrate Minimum"), Hours: hours}
	case hours <= u.OptimalHours:
		return UtilStatus{Key: "optimal", Label: labelOr(u.OptimalLabel, "Optimal"), Hours: hours}
	case hours < u.OverHours:
		return UtilStatus{Key: "high", Label: labelOr(u.HighLabel, "Zu hoch"), Hours: hours}
	default:
		return UtilStatus{Key: "over", Label: labelOr(u.OverLabel, "Überbucht"), Hours: hours}
	}
}

// labelOr returns s trimmed, or def if s is blank.
func labelOr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// IsHexColor reports whether s is a #RGB or #RRGGBB hex colour string.
func IsHexColor(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return false
		}
	}
	return true
}

// isHexDigit reports whether c is a hexadecimal digit (0-9, a-f, A-F).
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
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
// Projects belong to exactly one fiscal year (FiscalYear holds the FY anchor
// year); a project running across two fiscal years is created anew per FY.
//
// StartDate and EndDate (inclusive, ISO YYYY-MM-DD) bound the booking window:
// hours may only be planned/booked between them. An empty value defaults to the
// fiscal-year start/end respectively, so an unbounded project spans the whole FY.
type Project struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	BudgetHours float64 `json:"budgetHours"`
	Color       string  `json:"color"`
	Active      bool    `json:"active"`
	FiscalYear  int     `json:"fiscalYear"`
	StartDate   string  `json:"startDate,omitempty"` // inclusive, empty = FY start
	EndDate     string  `json:"endDate,omitempty"`   // inclusive, empty = FY end
	System      string  `json:"system,omitempty"`    // "" or "vacation" (auto-managed, non-deletable)
}

// Bookable reports whether the given ISO date lies within the project's booking
// window. Empty window bounds are treated as open (FY start/end). ISO date
// strings compare lexicographically, so no parsing is required.
func (p Project) Bookable(isoDate string) bool {
	if p.StartDate != "" && isoDate < p.StartDate {
		return false
	}
	if p.EndDate != "" && isoDate > p.EndDate {
		return false
	}
	return true
}

// ProjectsForFY returns the projects belonging to the given fiscal-year anchor.
func ProjectsForFY(ps []Project, year int) []Project {
	out := make([]Project, 0, len(ps))
	for _, p := range ps {
		if p.FiscalYear == year {
			out = append(out, p)
		}
	}
	return out
}

// VacationSystem marks the auto-managed, non-deletable vacation project of a
// fiscal year. Its hours are informational: they do not count towards the FY
// goal or the weekly utilization traffic-light.
const VacationSystem = "vacation"

// VacationColor is the fixed colour of the vacation project so it is visually
// distinct from real projects.
const VacationColor = "#64748b"

// vacationDayHours is the number of hours credited per planned vacation day.
const vacationDayHours = 8.0

// IsVacation reports whether the project is the auto-managed vacation project.
func (p Project) IsVacation() bool { return p.System == VacationSystem }

// VacationProjectID returns the stable ID of the vacation project of a FY.
func VacationProjectID(year int) string {
	return fmt.Sprintf("vacation-%d", year)
}

// VacationBudgetHours returns the vacation budget derived from the per-FY
// settings: (H1 + H2 vacation days) * 8h.
func (fy FiscalYearSettings) VacationBudgetHours() float64 {
	return float64(fy.VacationDaysH1+fy.VacationDaysH2) * vacationDayHours
}

// EnsureVacationProject makes sure a non-deletable vacation project exists for
// the given fiscal year and keeps its budget in sync with the FY settings
// (vacation days * 8h). It returns true when it created or changed anything so
// callers can decide whether to persist.
func EnsureVacationProject(d *Data, year int) bool {
	if !ValidYear(year) {
		return false
	}
	budget := d.FYFor(year).VacationBudgetHours()
	for i := range d.Projects {
		p := &d.Projects[i]
		if p.FiscalYear == year && p.IsVacation() {
			changed := false
			if p.BudgetHours != budget {
				p.BudgetHours = budget
				changed = true
			}
			if !p.Active {
				p.Active = true
				changed = true
			}
			if p.Name == "" {
				p.Name = "Urlaub"
				changed = true
			}
			return changed
		}
	}
	d.Projects = append(d.Projects, Project{
		ID:          VacationProjectID(year),
		Name:        "Urlaub",
		BudgetHours: budget,
		Color:       VacationColor,
		Active:      true,
		FiscalYear:  year,
		System:      VacationSystem,
	})
	return true
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
			FederalState:         "SN",
			WeeklyTargetHours:    40,
			FiscalYearStartMonth: 7,
			Utilization:          DefaultUtilization(),
		},
		FiscalYears: map[int]FiscalYearSettings{},
		Projects:    []Project{},
		Entries:     []Entry{},
	}
}

// DefaultFYSettings returns the default per-fiscal-year values used to pre-fill
// a fiscal year that has not been configured yet.
func DefaultFYSettings() FiscalYearSettings {
	return FiscalYearSettings{
		TargetHours:       1440,
		VacationDaysH1:    15,
		VacationDaysH2:    15,
		StandardTaskHours: 250,
	}
}

// FYFor returns the stored settings for the given fiscal-year anchor year. When
// no per-FY entry exists yet it falls back to the legacy global fields, or to
// the standard defaults, so an unconfigured year starts from sensible values.
func (d Data) FYFor(year int) FiscalYearSettings {
	if fy, ok := d.FiscalYears[year]; ok {
		return fy
	}
	if d.Settings.FiscalYearTargetHours > 0 || d.Settings.AnnualVacationDays > 0 {
		return FiscalYearSettings{
			TargetHours:    d.Settings.FiscalYearTargetHours,
			VacationDaysH1: d.Settings.AnnualVacationDays,
		}
	}
	return DefaultFYSettings()
}

// CurrentFY returns the per-FY settings for the active fiscal year.
func (d Data) CurrentFY() FiscalYearSettings {
	return d.FYFor(d.Settings.Year)
}

// Validate checks a document for structural and referential integrity. It is
// used before persisting data that was edited directly as JSON, so bad input
// is rejected instead of corrupting the store.
func Validate(d Data) error {
	if !ValidYear(d.Settings.Year) {
		return fmt.Errorf("settings.year %d liegt außerhalb von %d–%d", d.Settings.Year, MinYear, MaxYear)
	}
	if d.Settings.FiscalYearStartMonth != 0 && (d.Settings.FiscalYearStartMonth < 1 || d.Settings.FiscalYearStartMonth > 12) {
		return fmt.Errorf("settings.fiscalYearStartMonth %d muss zwischen 1 und 12 liegen", d.Settings.FiscalYearStartMonth)
	}
	if d.Settings.WeeklyTargetHours < 0 {
		return fmt.Errorf("settings.weeklyTargetHours darf nicht negativ sein")
	}
	if ep := strings.TrimSpace(d.Settings.AI.Endpoint); ep != "" && !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		return fmt.Errorf("settings.ai.endpoint muss mit http:// oder https:// beginnen")
	}
	u := d.Settings.Utilization
	if u.MinHours < 0 || u.OptimalHours < 0 || u.OverHours < 0 {
		return fmt.Errorf("settings.utilization: Schwellenwerte dürfen nicht negativ sein")
	}

	ids := make(map[string]bool, len(d.Projects))
	for i, p := range d.Projects {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("projects[%d]: id darf nicht leer sein", i)
		}
		if ids[p.ID] {
			return fmt.Errorf("projects[%d]: doppelte id %q", i, p.ID)
		}
		ids[p.ID] = true
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("projects[%d] (%s): name darf nicht leer sein", i, p.ID)
		}
		if p.BudgetHours < 0 {
			return fmt.Errorf("projects[%d] (%s): budgetHours darf nicht negativ sein", i, p.Name)
		}
		if p.System != "" && p.System != VacationSystem {
			return fmt.Errorf("projects[%d] (%s): system %q ist ungültig (nur %q erlaubt)", i, p.Name, p.System, VacationSystem)
		}
		if p.Color != "" && !IsHexColor(p.Color) {
			return fmt.Errorf("projects[%d] (%s): color %q ist keine gültige Hex-Farbe (#rgb oder #rrggbb)", i, p.Name, p.Color)
		}
		if p.FiscalYear != 0 && !ValidYear(p.FiscalYear) {
			return fmt.Errorf("projects[%d] (%s): fiscalYear %d liegt außerhalb von %d–%d", i, p.Name, p.FiscalYear, MinYear, MaxYear)
		}
		var ps, pe time.Time
		if strings.TrimSpace(p.StartDate) != "" {
			t, err := time.Parse("2006-01-02", p.StartDate)
			if err != nil {
				return fmt.Errorf("projects[%d] (%s): startDate %q ist kein gültiges Datum (YYYY-MM-DD)", i, p.Name, p.StartDate)
			}
			ps = t
		}
		if strings.TrimSpace(p.EndDate) != "" {
			t, err := time.Parse("2006-01-02", p.EndDate)
			if err != nil {
				return fmt.Errorf("projects[%d] (%s): endDate %q ist kein gültiges Datum (YYYY-MM-DD)", i, p.Name, p.EndDate)
			}
			pe = t
		}
		if !ps.IsZero() && !pe.IsZero() && pe.Before(ps) {
			return fmt.Errorf("projects[%d] (%s): endDate %q liegt vor startDate %q", i, p.Name, p.EndDate, p.StartDate)
		}
	}

	for i, e := range d.Entries {
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			return fmt.Errorf("entries[%d]: date %q ist kein gültiges Datum (YYYY-MM-DD)", i, e.Date)
		}
		if strings.TrimSpace(e.ProjectID) == "" {
			return fmt.Errorf("entries[%d] (%s): projectId darf nicht leer sein", i, e.Date)
		}
		if !ids[e.ProjectID] {
			return fmt.Errorf("entries[%d] (%s): projectId %q verweist auf kein existierendes Projekt", i, e.Date, e.ProjectID)
		}
		if e.Hours < 0 {
			return fmt.Errorf("entries[%d] (%s): hours darf nicht negativ sein", i, e.Date)
		}
		if e.Kind != "" && e.Kind != KindForecast && e.Kind != KindActual {
			return fmt.Errorf("entries[%d] (%s): kind %q muss %q oder %q sein", i, e.Date, e.Kind, KindForecast, KindActual)
		}
	}

	for year, fy := range d.FiscalYears {
		if !ValidYear(year) {
			return fmt.Errorf("fiscalYears: Schlüssel %d liegt außerhalb von %d–%d", year, MinYear, MaxYear)
		}
		if fy.TargetHours < 0 {
			return fmt.Errorf("fiscalYears[%d]: targetHours darf nicht negativ sein", year)
		}
		if fy.VacationDaysH1 < 0 || fy.VacationDaysH1 > 366 || fy.VacationDaysH2 < 0 || fy.VacationDaysH2 > 366 {
			return fmt.Errorf("fiscalYears[%d]: Urlaubstage müssen zwischen 0 und 366 liegen", year)
		}
		if fy.StandardTaskHours < 0 {
			return fmt.Errorf("fiscalYears[%d]: standardTaskHours darf nicht negativ sein", year)
		}
	}
	return nil
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
