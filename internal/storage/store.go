package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/daknoblo/forecast-tool/internal/models"
)

// Store wraps a JSON document on disk with concurrency-safe access.
type Store struct {
	mu   sync.RWMutex
	path string
	data models.Data
}

// New opens (or initializes) the store at the given file path.
func New(path string) (*Store, error) {
	s := &Store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		d := models.DefaultData(time.Now().Year())
		normalize(&d) // ensure defaults incl. the vacation project on first start
		s.data = d
		return s.persist()
	}
	if err != nil {
		return err
	}
	var d models.Data
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	normalize(&d)
	s.data = d
	return nil
}

// normalize fills in defaults and migrates legacy fields so the in-memory
// document is always self-consistent, whether loaded from disk or replaced via
// the JSON editor.
func normalize(d *models.Data) {
	if d.Projects == nil {
		d.Projects = []models.Project{}
	}
	if d.Entries == nil {
		d.Entries = []models.Entry{}
	}
	// Backwards compatibility: entries without a kind are planned forecasts.
	for i := range d.Entries {
		if d.Entries[i].Kind == "" {
			d.Entries[i].Kind = models.KindForecast
		}
	}
	if d.Settings.Year == 0 {
		d.Settings.Year = time.Now().Year()
	}
	if d.Settings.WeeklyTargetHours == 0 {
		d.Settings.WeeklyTargetHours = 40
	}
	if d.Settings.FederalState == "" {
		d.Settings.FederalState = "SN"
	}
	if d.Settings.FiscalYearStartMonth < 1 || d.Settings.FiscalYearStartMonth > 12 {
		d.Settings.FiscalYearStartMonth = 7
	}
	// Default the utilization traffic-light for documents created before it
	// existed (all thresholds zero == unset).
	if d.Settings.Utilization.MinHours == 0 && d.Settings.Utilization.OptimalHours == 0 && d.Settings.Utilization.OverHours == 0 {
		d.Settings.Utilization = models.DefaultUtilization()
	}
	if d.FiscalYears == nil {
		d.FiscalYears = map[int]models.FiscalYearSettings{}
	}
	// Migrate legacy projects without a fiscal year to the active FY so they
	// remain visible after projects became fiscal-year scoped.
	for i := range d.Projects {
		if d.Projects[i].FiscalYear == 0 {
			d.Projects[i].FiscalYear = d.Settings.Year
		}
	}
	// Migrate legacy single-FY settings into the per-FY map on first load.
	if _, ok := d.FiscalYears[d.Settings.Year]; !ok {
		if d.Settings.FiscalYearTargetHours > 0 || d.Settings.AnnualVacationDays > 0 {
			d.FiscalYears[d.Settings.Year] = models.FiscalYearSettings{
				TargetHours:    d.Settings.FiscalYearTargetHours,
				VacationDaysH1: d.Settings.AnnualVacationDays,
			}
		}
	}
	// Ensure the non-deletable vacation project exists for the active FY and
	// that its budget matches the configured vacation days. This also enforces
	// the project even when a user removes it via the raw JSON editor.
	models.EnsureVacationProject(d, d.Settings.Year)
}

// persist writes the current document atomically (temp file + rename).
// Caller must hold the write lock.
func (s *Store) persist() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot returns a deep-ish copy of the data safe for read-only use.
func (s *Store) Snapshot() models.Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.data
	d.Projects = append([]models.Project(nil), s.data.Projects...)
	d.Entries = append([]models.Entry(nil), s.data.Entries...)
	d.FiscalYears = make(map[int]models.FiscalYearSettings, len(s.data.FiscalYears))
	for k, v := range s.data.FiscalYears {
		d.FiscalYears[k] = v
	}
	return d
}

// Path returns the on-disk path of the data file.
func (s *Store) Path() string { return s.path }

// FileSize returns the size in bytes of the data file on disk (0 if missing).
func (s *Store) FileSize() int64 {
	if fi, err := os.Stat(s.path); err == nil {
		return fi.Size()
	}
	return 0
}

// Marshal returns the current document as indented JSON for export/download.
func (s *Store) Marshal() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.MarshalIndent(s.data, "", "  ")
}

// ReplaceJSON parses raw JSON, validates it, normalizes defaults and atomically
// replaces the whole document. The data is only persisted when it is valid, so
// a bad payload never corrupts the store.
func (s *Store) ReplaceJSON(raw []byte) error {
	d, err := parseAndValidate(raw)
	if err != nil {
		return err
	}
	normalize(&d)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = d
	return s.persist()
}

// ValidateJSON parses and validates raw JSON without persisting anything. It is
// used to check AI-generated output before offering it for saving.
func (s *Store) ValidateJSON(raw []byte) error {
	_, err := parseAndValidate(raw)
	return err
}

// parseAndValidate decodes raw JSON strictly and runs model validation.
func parseAndValidate(raw []byte) (models.Data, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d models.Data
	if err := dec.Decode(&d); err != nil {
		return models.Data{}, fmt.Errorf("ungültiges JSON: %w", err)
	}
	if dec.More() {
		return models.Data{}, fmt.Errorf("ungültiges JSON: zusätzliche Daten nach dem JSON-Objekt")
	}
	if err := models.Validate(d); err != nil {
		return models.Data{}, err
	}
	return d, nil
}

// Reset clears all bookings (entries) and projects while preserving every
// setting (global Settings and per-fiscal-year FiscalYears). The year argument
// is unused and kept only for backward compatibility of the signature.
func (s *Store) Reset(year int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Projects = []models.Project{}
	s.data.Entries = []models.Entry{}
	normalize(&s.data)
	return s.persist()
}

// Update runs fn against the mutable data under the write lock and persists.
func (s *Store) Update(fn func(d *models.Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.data); err != nil {
		return err
	}
	return s.persist()
}
