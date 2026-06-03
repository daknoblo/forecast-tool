package models

// Settings holds global configuration for a single calendar year.
type Settings struct {
	Year              int     `json:"year"`
	FederalState      string  `json:"federalState"` // e.g. "BY", "BW", "BE" ...
	WeeklyTargetHours float64 `json:"weeklyTargetHours"`
}

// Project is a thing time is forecasted against, constrained by a budget.
type Project struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	BudgetHours float64 `json:"budgetHours"`
	Color       string  `json:"color"`
	Active      bool    `json:"active"`
}

// Entry is the number of forecasted hours for a project on a specific day.
// Date is stored as ISO date string (YYYY-MM-DD).
type Entry struct {
	Date      string  `json:"date"`
	ProjectID string  `json:"projectId"`
	Hours     float64 `json:"hours"`
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
			Year:              year,
			FederalState:      "BY",
			WeeklyTargetHours: 40,
		},
		Projects: []Project{},
		Entries:  []Entry{},
	}
}
