package holidays

import (
	"sort"
	"time"

	"github.com/rickar/cal/v2"
	"github.com/rickar/cal/v2/de"
)

// State represents a German federal state selectable in the settings.
type State struct {
	Code string
	Name string
}

// States lists all German federal states in display order.
var States = []State{
	{"BW", "Baden-Württemberg"},
	{"BY", "Bayern"},
	{"BE", "Berlin"},
	{"BB", "Brandenburg"},
	{"HB", "Bremen"},
	{"HH", "Hamburg"},
	{"HE", "Hessen"},
	{"MV", "Mecklenburg-Vorpommern"},
	{"NI", "Niedersachsen"},
	{"NW", "Nordrhein-Westfalen"},
	{"RP", "Rheinland-Pfalz"},
	{"SL", "Saarland"},
	{"SN", "Sachsen"},
	{"ST", "Sachsen-Anhalt"},
	{"SH", "Schleswig-Holstein"},
	{"TH", "Thüringen"},
}

func regionHolidays(state string) []*cal.Holiday {
	switch state {
	case "BW":
		return de.HolidaysBW
	case "BY":
		return de.HolidaysBY
	case "BE":
		return de.HolidaysBE
	case "BB":
		return de.HolidaysBB
	case "HB":
		return de.HolidaysHB
	case "HH":
		return de.HolidaysHH
	case "HE":
		return de.HolidaysHE
	case "MV":
		return de.HolidaysMV
	case "NI":
		return de.HolidaysNI
	case "NW":
		return de.HolidaysNW
	case "RP":
		return de.HolidaysRP
	case "SL":
		return de.HolidaysSL
	case "SN":
		return de.HolidaysSN
	case "ST":
		return de.HolidaysST
	case "SH":
		return de.HolidaysSH
	case "TH":
		return de.HolidaysTH
	default:
		return de.Holidays
	}
}

// Calendar resolves public holidays for a given year and federal state.
type Calendar struct {
	byDate map[string]string // YYYY-MM-DD -> holiday name
}

// New builds a holiday lookup for the given year and state code.
func New(year int, state string) *Calendar {
	bc := cal.NewBusinessCalendar()
	bc.AddHoliday(regionHolidays(state)...)

	byDate := make(map[string]string)
	d := time.Date(year, time.January, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	for d.Before(end) {
		if _, _, h := bc.IsHoliday(d); h != nil {
			byDate[d.Format("2006-01-02")] = h.Name
		}
		d = d.AddDate(0, 0, 1)
	}
	return &Calendar{byDate: byDate}
}

// IsHoliday reports whether the given ISO date (YYYY-MM-DD) is a public holiday.
func (c *Calendar) IsHoliday(isoDate string) bool {
	_, ok := c.byDate[isoDate]
	return ok
}

// Name returns the holiday name for the given ISO date, or "".
func (c *Calendar) Name(isoDate string) string {
	return c.byDate[isoDate]
}

// All returns the holidays of the year sorted by date.
func (c *Calendar) All() []struct {
	Date string
	Name string
} {
	out := make([]struct {
		Date string
		Name string
	}, 0, len(c.byDate))
	for d, n := range c.byDate {
		out = append(out, struct {
			Date string
			Name string
		}{d, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
