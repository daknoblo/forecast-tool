package forecast

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/holidays"
	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestMonthHistoryAdaptsToRecentRoutine(t *testing.T) {
	var data, available monthHistoryWeeksData
	for week := range data {
		available[week] = [5]float64{1, 1, 1, 1, 1}
		if week < 8 {
			data[week][0] = 8
		} else {
			data[week][4] = 8
		}
	}
	uniform := [5]float64{0.2, 0.2, 0.2, 0.2, 0.2}
	pattern, weeks := monthHistoryPattern(data, available, uniform)
	// Hold out a week in the new Friday routine. The former raw-hour model
	// predicts 2/3 Monday and 1/3 Friday from these twelve training weeks.
	oldError := 2.0 / 3
	newError := (math.Abs(pattern[0]) + math.Abs(1-pattern[4])) / 2
	if weeks != 12 || pattern[4] <= pattern[0] || newError >= oldError*0.7 {
		t.Fatalf("recent routine not learned: pattern=%v, error=%g, old=%g", pattern, newError, oldError)
	}
	data[0][0] = 800
	scaled, _ := monthHistoryPattern(data, available, uniform)
	for i := range pattern {
		if math.Abs(pattern[i]-scaled[i]) > 1e-9 {
			t.Fatal("one large historical booking changed the weekday pattern")
		}
	}
}

func TestMonthHistoryNeutralizesAbsencesAndSparseEvidence(t *testing.T) {
	var data, available monthHistoryWeeksData
	uniform := [5]float64{0.2, 0.2, 0.2, 0.2, 0.2}
	for week := range data {
		available[week] = [5]float64{1, 1, 1, 1, 1}
		// Alternate full and half Monday absences; the actual routine is even.
		available[week][0] = float64(week%2) / 2
		for day := range data[week] {
			data[week][day] = 8 * available[week][day]
		}
	}
	pattern, _ := monthHistoryPattern(data, available, uniform)
	for _, weight := range pattern {
		if math.Abs(weight-0.2) > 1e-9 {
			t.Fatalf("absence learned as a weekday preference: %v", pattern)
		}
	}
	data = monthHistoryWeeksData{}
	data[11][4] = 8
	pattern, weeks := monthHistoryPattern(data, available, uniform)
	if weeks != 1 || pattern[4] <= 0.2 || pattern[4] >= 0.5 || pattern[1] <= 0 {
		t.Fatalf("one observation must be blended, not treated as a fixed schedule: %v", pattern)
	}
}

func TestMonthHistoryPersonalFallbackAndTrainingBounds(t *testing.T) {
	d := monthTestData()
	d.Projects = append(d.Projects, models.Project{ID: "new", Name: "New"})
	end := monthTestDate("2026-03-02")
	for week := 1; week <= 4; week++ {
		d.Entries = append(d.Entries, models.Entry{
			Date: end.AddDate(0, 0, -week*7).Format("2006-01-02"), ProjectID: "p", Hours: 8,
		})
	}
	build := func() map[string]monthHistory {
		return buildMonthHistories(d.Projects, hoursIndex(d.Entries), end, "SN")
	}
	baseline := build()
	newHistory := baseline[groupKey(d.Projects[2])]
	if newHistory.weights[0] != 1 || !strings.Contains(newHistory.basis, "Persönliches") {
		t.Fatalf("new project did not inherit personal routine: %+v", newHistory)
	}
	d.Entries = append(d.Entries, []models.Entry{
		{Date: "2026-03-03", ProjectID: "p", Hours: 500},
		{Date: "2026-04-03", ProjectID: "p", Hours: 500},
		{Date: "2026-02-28", ProjectID: "p", Hours: 500},
		{Date: "2026-02-24", ProjectID: "missing", Hours: 500},
		{Date: "2025-11-03", ProjectID: "p", Hours: 500},
		{Date: "2026-01-01", ProjectID: "p", Hours: 500},
		{Date: "2026-02-20", ProjectID: "v", Hours: 8},
		{Date: "2026-02-20", ProjectID: "p", Hours: 500},
	}...)
	if !reflect.DeepEqual(baseline, build()) {
		t.Fatal("unavailable days, orphan entries or nonhistorical data changed the model")
	}
}

func TestMonthHistoryReservesPreferredDaysBeforeFlexibleProjects(t *testing.T) {
	d := monthTestData()
	d.Projects = []models.Project{
		{ID: "a-flexible", Name: "Flexible"},
		{ID: "z-monday", Name: "Monday"},
	}
	now := monthTestDate("2026-03-02")
	for week := 1; week <= 4; week++ {
		monday := now.AddDate(0, 0, -week*7)
		d.Entries = append(d.Entries, models.Entry{Date: monday.Format("2006-01-02"), ProjectID: "z-monday", Hours: 4})
		for day := 0; day < 5; day++ {
			d.Entries = append(d.Entries, models.Entry{Date: monday.AddDate(0, 0, day).Format("2006-01-02"), ProjectID: "a-flexible", Hours: 4})
		}
	}
	d.Entries = append(d.Entries,
		models.Entry{Date: "2026-03-06", ProjectID: "a-flexible", Hours: 32},
		models.Entry{Date: "2026-03-06", ProjectID: "z-monday", Hours: 8},
	)
	plan := BuildMonthPlan(d, holidays.Get(2026, "SN"), now, now)
	w := monthTestWeek(t, plan, "2026-03-02")
	if w.Unallocated != 0 || len(w.Days[0].Events) != 1 || w.Days[0].Events[0].ProjectID != "z-monday" || w.Days[0].Total != 8 {
		t.Fatalf("flexible project displaced the recurring Monday booking: %+v", w)
	}
}
