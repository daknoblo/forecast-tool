package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/models"
)

func TestExpandPlanGeneratesWeekdayEntries(t *testing.T) {
	raw := []byte(`{
      "settings": { "year": 2027, "federalState": "SN", "weeklyTargetHours": 40, "fiscalYearStartMonth": 7, "ai": {"endpoint":"","deployment":"","apiVersion":""} },
      "fiscalYears": { "2027": { "targetHours": 1440, "vacationDaysH1": 15, "vacationDaysH2": 15, "standardTaskLabel": "", "standardTaskHours": 250 } },
      "projects": [ { "id": "mvp", "name": "MVP", "budgetHours": 250, "color": "#2563eb", "active": true, "fiscalYear": 2027 } ],
      "entries": [],
      "forecastPlan": [ { "projectId": "mvp", "fiscalYear": 2027, "hoursPerWeek": 20, "kind": "forecast" } ]
    }`)

	out, err := ExpandPlan(raw, 7)
	if err != nil {
		t.Fatalf("ExpandPlan: %v", err)
	}
	if strings.Contains(string(out), "forecastPlan") {
		t.Fatalf("expanded output still contains forecastPlan")
	}

	var data models.Data
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("unmarshal expanded: %v", err)
	}
	if err := models.Validate(data); err != nil {
		t.Fatalf("expanded document invalid: %v", err)
	}
	// FY 2027 (Jul 2027 – Jun 2028) has ~261 weekdays.
	if len(data.Entries) < 250 || len(data.Entries) > 270 {
		t.Fatalf("unexpected entry count: %d", len(data.Entries))
	}
	for _, e := range data.Entries {
		if e.ProjectID != "mvp" || e.Kind != models.KindForecast || e.Hours != 4 {
			t.Fatalf("unexpected entry: %+v", e)
		}
	}
}

func TestExpandPlanNoPlanReturnsInput(t *testing.T) {
	raw := []byte(`{"settings":{"year":2027},"projects":[],"entries":[]}`)
	out, err := ExpandPlan(raw, 7)
	if err != nil {
		t.Fatalf("ExpandPlan: %v", err)
	}
	if string(out) != string(raw) {
		t.Fatalf("expected unchanged input when no forecastPlan present")
	}
}
