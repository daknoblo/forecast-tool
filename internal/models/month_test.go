package models

import (
	"strings"
	"testing"
)

func TestMonthPlanningValidation(t *testing.T) {
	d := DefaultData(2026)
	d.Settings.MonthPlanningPrompt = strings.Repeat("ä", MaxMonthPlanningPrompt)
	d.SavedMonthPlans = map[string]string{"2026-09": "2026-09-23T12:00:00Z"}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	d.Settings.MonthPlanningPrompt += "a"
	if err := Validate(d); err == nil {
		t.Fatal("oversized prompt accepted")
	}
	d.Settings.MonthPlanningPrompt = ""
	d.Settings.MonthPlanningSystemPrompt = strings.Repeat("ä", MaxMonthPlanningPrompt)
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	d.Settings.MonthPlanningSystemPrompt += "a"
	if err := Validate(d); err == nil {
		t.Fatal("oversized system prompt accepted")
	}
	d.Settings.MonthPlanningSystemPrompt = ""
	for _, value := range []map[string]string{
		{"2026-13": "2026-09-23T12:00:00Z"},
		{"2026-09": "not a timestamp"},
	} {
		d.SavedMonthPlans = value
		if err := Validate(d); err == nil {
			t.Fatalf("invalid saved-month metadata accepted: %v", value)
		}
	}
}
