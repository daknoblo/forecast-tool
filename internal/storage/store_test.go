package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/models"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, path
}

// A fresh store must come up with a usable document: a fiscal year, the
// traffic-light defaults and the auto-managed vacation project.
func TestNewStoreIsSelfConsistent(t *testing.T) {
	s, path := newStore(t)
	d := s.Snapshot()

	if !models.ValidYear(d.Settings.Year) {
		t.Errorf("year = %d, want a plausible fiscal year", d.Settings.Year)
	}
	if d.Settings.WeeklyTargetHours <= 0 || d.Settings.FederalState == "" {
		t.Errorf("settings not defaulted: %+v", d.Settings)
	}
	if u := d.Settings.Utilization; u.MinHours == 0 && u.OptimalHours == 0 && u.OverHours == 0 {
		t.Error("utilization thresholds were left unset")
	}
	var vac int
	for _, p := range d.Projects {
		if p.IsVacation() && p.FiscalYear == d.Settings.Year {
			vac++
		}
	}
	if vac != 1 {
		t.Errorf("%d vacation projects for the active FY, want exactly 1", vac)
	}
	if err := models.Validate(d); err != nil {
		t.Errorf("a fresh document does not validate: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the store did not persist itself: %v", err)
	}
}

// Legacy documents carried separate forecast and actual entries. Loading one
// has to collapse them to a single value per day and project, with the booked
// actual winning over the forecast.
func TestLoadCollapsesLegacyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	raw := models.Data{
		Settings: models.Settings{Year: 2027, FederalState: "BY", WeeklyTargetHours: 40, FiscalYearStartMonth: 7},
		Projects: []models.Project{
			{ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 100, Active: true, FiscalYear: 2027},
		},
		Entries: []models.Entry{
			{Date: "2026-09-01", ProjectID: "p1", Hours: 6, Kind: models.KindForecast},
			{Date: "2026-09-01", ProjectID: "p1", Hours: 8, Kind: models.KindActual},
			{Date: "2026-09-02", ProjectID: "p1", Hours: 2, Kind: models.KindForecast},
			{Date: "2026-09-02", ProjectID: "p1", Hours: 3, Kind: models.KindForecast},
			{Date: "2026-09-03", ProjectID: "p1", Hours: 0, Kind: models.KindForecast},
		},
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := map[string]float64{}
	for _, e := range s.Snapshot().Entries {
		if e.Kind != "" {
			t.Errorf("entry %s keeps the legacy kind %q", e.Date, e.Kind)
		}
		if _, dup := got[e.Date]; dup {
			t.Errorf("date %s appears twice after the merge", e.Date)
		}
		got[e.Date] = e.Hours
	}
	if got["2026-09-01"] != 8 {
		t.Errorf("01.09. = %v, want 8 (the booked actual wins)", got["2026-09-01"])
	}
	if got["2026-09-02"] != 5 {
		t.Errorf("02.09. = %v, want 5 (same-kind hours are summed)", got["2026-09-02"])
	}
	if _, ok := got["2026-09-03"]; ok {
		t.Error("a zero-hour entry survived the merge")
	}
}

// Mutate is the safe write path: a rejected change must leave neither the
// in-memory document nor the file behind in a half-applied state.
func TestMutateRollsBackOnInvalidData(t *testing.T) {
	s, path := newStore(t)
	before := s.Snapshot()

	err := s.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "broken", Name: "", BudgetHours: -5, Active: true, FiscalYear: d.Settings.Year,
		})
		return nil
	})
	if err == nil {
		t.Fatal("Mutate accepted a project without a name and with a negative budget")
	}
	if got := len(s.Snapshot().Projects); got != len(before.Projects) {
		t.Errorf("%d projects after the failed write, want %d", got, len(before.Projects))
	}

	b, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	var onDisk models.Data
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatalf("the file is no longer valid JSON: %v", err)
	}
	for _, p := range onDisk.Projects {
		if p.ID == "broken" {
			t.Error("the rejected project was persisted anyway")
		}
	}
}

// Changing the vacation days has to move the vacation project's budget with
// them, without touching anything the user configured.
func TestVacationBudgetFollowsTheSettings(t *testing.T) {
	s, _ := newStore(t)
	year := s.Snapshot().Settings.Year

	if err := s.Mutate(func(d *models.Data) error {
		d.FiscalYears[year] = models.FiscalYearSettings{VacationDays: 27}
		return nil
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	for _, p := range s.Snapshot().Projects {
		if p.IsVacation() && p.FiscalYear == year {
			if p.BudgetHours != 27*8 {
				t.Errorf("vacation budget = %v, want %v", p.BudgetHours, 27*8)
			}
			if p.Color != models.VacationColor || p.Name == "" {
				t.Errorf("vacation project lost its identity: %+v", p)
			}
		}
	}
}

// A stored document has to survive a reload byte for byte in meaning.
func TestPersistAndReloadRoundTrip(t *testing.T) {
	s, path := newStore(t)
	year := s.Snapshot().Settings.Year

	if err := s.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "5641245", Name: "Alpha", BudgetHours: 120.5,
			Color: "#2563eb", Active: true, FiscalYear: year,
			StartDate: "2026-09-01", EndDate: "2027-03-31",
		})
		d.Entries = append(d.Entries, models.Entry{Date: "2026-09-01", ProjectID: "p1", Hours: 7.5})
		return nil
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	want := s.Snapshot()

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.Snapshot()

	if len(got.Projects) != len(want.Projects) || len(got.Entries) != len(want.Entries) {
		t.Fatalf("reloaded %d projects / %d entries, want %d/%d",
			len(got.Projects), len(got.Entries), len(want.Projects), len(want.Entries))
	}
	for i := range want.Projects {
		if got.Projects[i] != want.Projects[i] {
			t.Errorf("project %d changed on reload:\n got %+v\nwant %+v", i, got.Projects[i], want.Projects[i])
		}
	}
	for i := range want.Entries {
		if got.Entries[i] != want.Entries[i] {
			t.Errorf("entry %d changed on reload: got %+v, want %+v", i, got.Entries[i], want.Entries[i])
		}
	}
	if got.FiscalYears[year] != want.FiscalYears[year] {
		t.Errorf("FY settings changed on reload: got %+v, want %+v", got.FiscalYears[year], want.FiscalYears[year])
	}
}

// A snapshot is a copy: mutating it must never reach the store.
func TestSnapshotIsIsolated(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Mutate(func(d *models.Data) error {
		d.Projects = append(d.Projects, models.Project{
			ID: "p1", AssignmentID: "1", Name: "Alpha", BudgetHours: 10,
			Color: "#2563eb", Active: true, FiscalYear: d.Settings.Year,
		})
		return nil
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	snap := s.Snapshot()
	snap.Projects[0].Name = "tampered"
	snap.Projects = append(snap.Projects, models.Project{ID: "extra"})
	snap.FiscalYears[9999] = models.FiscalYearSettings{VacationDays: 1}

	fresh := s.Snapshot()
	if fresh.Projects[0].Name == "tampered" {
		t.Error("mutating a snapshot changed the store")
	}
	if len(fresh.Projects) != len(snap.Projects)-1 {
		t.Error("appending to a snapshot changed the store")
	}
	if _, ok := fresh.FiscalYears[9999]; ok {
		t.Error("writing to a snapshot's FY map changed the store")
	}
}
