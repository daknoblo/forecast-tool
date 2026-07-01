package models

import "testing"

func TestClassifyUtilization(t *testing.T) {
	s := Settings{Utilization: DefaultUtilization()} // 26 / 40 / 60
	cases := []struct {
		hours float64
		key   string
	}{
		{0, "min"},
		{26, "min"},
		{26.5, "optimal"},
		{40, "optimal"},
		{41, "high"},
		{59, "high"},
		{59.9, "high"},
		{60, "over"},
		{80, "over"},
	}
	for _, c := range cases {
		if got := s.ClassifyUtilization(c.hours); got.Key != c.key {
			t.Errorf("ClassifyUtilization(%v) = %q, want %q", c.hours, got.Key, c.key)
		}
	}
}

func TestClassifyUtilizationFallback(t *testing.T) {
	// All-zero thresholds fall back to the defaults.
	var s Settings
	if got := s.ClassifyUtilization(40); got.Key != "optimal" {
		t.Errorf("fallback ClassifyUtilization(40) = %q, want optimal", got.Key)
	}
	if got := s.ClassifyUtilization(70); got.Key != "over" {
		t.Errorf("fallback ClassifyUtilization(70) = %q, want over", got.Key)
	}
}

func TestEnsureVacationProject(t *testing.T) {
	d := Data{
		Settings:    Settings{Year: 2026, FiscalYearStartMonth: 7},
		FiscalYears: map[int]FiscalYearSettings{2026: {TargetHours: 1000, VacationDaysH1: 10, VacationDaysH2: 5}},
	}
	// First call creates the project with budget (10+5)*8 = 120.
	if !EnsureVacationProject(&d, 2026) {
		t.Fatal("expected EnsureVacationProject to report a change on first call")
	}
	if len(d.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(d.Projects))
	}
	vp := d.Projects[0]
	if !vp.IsVacation() {
		t.Error("created project is not marked as vacation")
	}
	if vp.ID != VacationProjectID(2026) {
		t.Errorf("id = %q, want %q", vp.ID, VacationProjectID(2026))
	}
	if vp.BudgetHours != 120 {
		t.Errorf("budget = %v, want 120", vp.BudgetHours)
	}
	// Idempotent: a second call with unchanged settings makes no change.
	if EnsureVacationProject(&d, 2026) {
		t.Error("expected no change on second call")
	}
	if len(d.Projects) != 1 {
		t.Errorf("projects = %d after second call, want 1", len(d.Projects))
	}
	// Changing vacation days re-syncs the budget.
	d.FiscalYears[2026] = FiscalYearSettings{TargetHours: 1000, VacationDaysH1: 20, VacationDaysH2: 5}
	if !EnsureVacationProject(&d, 2026) {
		t.Error("expected change after vacation days increased")
	}
	if d.Projects[0].BudgetHours != 200 { // (20+5)*8
		t.Errorf("budget = %v, want 200", d.Projects[0].BudgetHours)
	}
}

func TestValidateRejectsBadColorAndSystem(t *testing.T) {
	base := func() Data {
		return Data{
			Settings:    Settings{Year: 2026, FiscalYearStartMonth: 7, WeeklyTargetHours: 40, Utilization: DefaultUtilization()},
			FiscalYears: map[int]FiscalYearSettings{},
			Projects:    []Project{{ID: "p1", Name: "Alpha", BudgetHours: 10, Color: "#2563eb", FiscalYear: 2026}},
			Entries:     []Entry{},
		}
	}
	if err := Validate(base()); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	bad := base()
	bad.Projects[0].Color = "red; }</style>"
	if err := Validate(bad); err == nil {
		t.Error("expected invalid colour to be rejected")
	}
	badSys := base()
	badSys.Projects[0].System = "admin"
	if err := Validate(badSys); err == nil {
		t.Error("expected invalid system value to be rejected")
	}
	okVac := base()
	okVac.Projects[0].System = VacationSystem
	if err := Validate(okVac); err != nil {
		t.Errorf("vacation system value should be valid: %v", err)
	}
}

func TestIsHexColor(t *testing.T) {
	valid := []string{"#fff", "#000000", "#2563EB", "#abcdef"}
	invalid := []string{"", "fff", "#ff", "#gggggg", "#12345", "red", "#2563eb;"}
	for _, c := range valid {
		if !IsHexColor(c) {
			t.Errorf("IsHexColor(%q) = false, want true", c)
		}
	}
	for _, c := range invalid {
		if IsHexColor(c) {
			t.Errorf("IsHexColor(%q) = true, want false", c)
		}
	}
}
