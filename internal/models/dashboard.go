package models

// DashboardRange defines a selectable horizon for the dashboard charts.
type DashboardRange struct {
	Key   string
	Label string
}

// DashboardRanges lists the horizons in display order for settings and charts.
var DashboardRanges = []DashboardRange{
	{Key: "1w", Label: "1 Woche"},
	{Key: "2w", Label: "2 Wochen"},
	{Key: "4w", Label: "4 Wochen"},
	{Key: "2m", Label: "2 Monate"},
	{Key: "3m", Label: "3 Monate"},
	{Key: "6m", Label: "Halbjahr"},
	{Key: "fy", Label: "Fiskaljahr"},
}

const DefaultDashboardRange = "4w"

// ValidDashboardRange reports whether key is a supported dashboard horizon.
func ValidDashboardRange(key string) bool {
	for _, r := range DashboardRanges {
		if r.Key == key {
			return true
		}
	}
	return false
}
