package forecast

import (
	"fmt"
	"math"
	"time"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

const monthHistoryWeeks = 12

var monthWeekdayNames = [...]string{"Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"}

type monthHistory struct {
	weights [5]float64
	basis   string
}

type monthHistoryWeeksData [monthHistoryWeeks][5]float64

func buildMonthHistories(projects []models.Project, index map[string]float64, end time.Time, state string) map[string]monthHistory {
	start := end.AddDate(0, 0, -7*monthHistoryWeeks)
	cal := holidays.Get(end.Year(), state)
	var available, all monthHistoryWeeksData
	byAssignment := make(map[string]*monthHistoryWeeksData)
	for week := range available {
		for day := range available[week] {
			iso := start.AddDate(0, 0, week*7+day).Format("2006-01-02")
			if cal.IsHoliday(iso) {
				continue
			}
			var vacation float64
			for _, p := range projects {
				if p.IsVacation() {
					vacation += index[iso+"|"+p.ID]
				}
			}
			available[week][day] = math.Max(0, 1-vacation/HolidayDayHours)
			if available[week][day] == 0 {
				continue
			}
			for _, p := range projects {
				h := index[iso+"|"+p.ID]
				if p.IsVacation() || h <= 0 {
					continue
				}
				key := groupKey(p)
				if byAssignment[key] == nil {
					byAssignment[key] = &monthHistoryWeeksData{}
				}
				byAssignment[key][week][day] += h
				all[week][day] += h
			}
		}
	}
	uniform := [5]float64{0.2, 0.2, 0.2, 0.2, 0.2}
	general, generalWeeks := monthHistoryPattern(all, available, uniform)
	result := make(map[string]monthHistory)
	for _, p := range projects {
		key := groupKey(p)
		if _, exists := result[key]; exists || p.IsVacation() {
			continue
		}
		h := monthHistory{weights: general, basis: "Gleichmäßig: keine verwertbare Buchungshistorie"}
		if generalWeeks > 0 {
			h.basis = fmt.Sprintf("Persönliches Wochenmuster aus %d Wochen; neuere Buchungen stärker gewichtet", generalWeeks)
		}
		if data := byAssignment[key]; data != nil {
			var weeks int
			h.weights, weeks = monthHistoryPattern(*data, available, general)
			h.basis = fmt.Sprintf("Projektmuster aus %d Wochen; neuere Buchungen stärker gewichtet", weeks)
			if weeks < 4 {
				h.basis += "; mit persönlichem Wochenmuster abgesichert"
			}
		}
		result[key] = h
	}
	return result
}

// Every observed week contributes its shares, not its absolute hours, so a
// single large booking cannot outweigh many recurring weeks. Availability
// neutralizes holidays and full/partial vacation instead of learning them as
// habitual days off. Four observed weeks replace the fallback completely.
func monthHistoryPattern(data, available monthHistoryWeeksData, fallback [5]float64) ([5]float64, int) {
	var shares, exposure [5]float64
	observed := 0
	for week, days := range data {
		var total, capacity float64
		for day, hours := range days {
			total += hours
			capacity += available[week][day]
		}
		if total <= 0 {
			continue
		}
		observed++
		weight := math.Exp2(-float64(monthHistoryWeeks-1-week) / 4)
		for day, hours := range days {
			shares[day] += weight * hours / total * capacity
			exposure[day] += weight * available[week][day]
		}
	}
	if observed == 0 {
		return fallback, 0
	}
	var pattern [5]float64
	var sum float64
	for day := range pattern {
		pattern[day] = fallback[day] * 5
		if exposure[day] > 0 {
			pattern[day] = shares[day] / exposure[day]
		}
		sum += pattern[day]
	}
	confidence := math.Min(1, float64(observed)/4)
	for day := range pattern {
		pattern[day] = confidence*pattern[day]/sum + (1-confidence)*fallback[day]
	}
	return pattern, observed
}
