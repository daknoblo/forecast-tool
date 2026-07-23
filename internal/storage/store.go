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
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
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
	// Collapse entries to a single hours value per (date, projectId). Legacy
	// documents distinguished forecast and actual entries; a booked actual used
	// to override the forecast for the same day and project, so it wins here too.
	// The kind marker is dropped afterwards: hours are now classified as booked
	// or forecast purely by their date at read time.
	d.Entries = mergeEntries(d.Entries)
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

// mergeEntries collapses entries to a single value per (date, projectId). A
// legacy booked "actual" entry overrides the "forecast" for the same day and
// project (the old effective-hours rule); otherwise same-key hours are summed.
// The resulting entries carry no kind and zero-hour results are dropped. The
// first-seen order of (date, projectId) pairs is preserved for a stable file.
func mergeEntries(entries []models.Entry) []models.Entry {
	type key struct{ date, project string }
	forecast := map[key]float64{}
	actual := map[key]float64{}
	hasActual := map[key]bool{}
	order := make([]key, 0, len(entries))
	seen := map[key]bool{}
	for _, e := range entries {
		k := key{e.Date, e.ProjectID}
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
		if e.Kind == models.KindActual {
			actual[k] += e.Hours
			hasActual[k] = true
		} else {
			forecast[k] += e.Hours
		}
	}
	out := make([]models.Entry, 0, len(order))
	for _, k := range order {
		h := forecast[k]
		if hasActual[k] {
			h = actual[k]
		}
		if h == 0 {
			continue
		}
		out = append(out, models.Entry{Date: k.date, ProjectID: k.project, Hours: h})
	}
	return out
}

// persist writes the current document atomically (temp file + rename).
// Caller must hold the write lock.
func (s *Store) persist() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot returns a deep-ish copy of the data safe for read-only use.
func (s *Store) Snapshot() models.Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.data)
}

// clone returns a deep-ish copy of a document: the slices and the FiscalYears
// map are copied so a caller can mutate the result without touching the shared
// (or another) document. Nested values are all value types, so this is enough.
func clone(src models.Data) models.Data {
	d := src
	d.Projects = append([]models.Project(nil), src.Projects...)
	d.Entries = append([]models.Entry(nil), src.Entries...)
	d.FiscalYears = make(map[int]models.FiscalYearSettings, len(src.FiscalYears))
	for k, v := range src.FiscalYears {
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

// Mutate applies fn to a working copy of the document, normalizes it, validates
// the result and only then swaps it in and persists. Unlike Update, a failing
// mutation (either fn returning an error or validation rejecting the result)
// never leaves the in-memory document in a half-changed state, which makes it
// the safe workhorse for the JSON API's write endpoints.
func (s *Store) Mutate(fn func(d *models.Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	working := clone(s.data)
	if err := fn(&working); err != nil {
		return err
	}
	normalize(&working)
	if err := models.Validate(working); err != nil {
		return err
	}
	s.data = working
	return s.persist()
}
