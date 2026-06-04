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
