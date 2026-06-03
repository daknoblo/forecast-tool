package storage

import (
	"encoding/json"
	"errors"
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
		s.data = models.DefaultData(time.Now().Year())
		return s.persist()
	}
	if err != nil {
		return err
	}
	var d models.Data
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	if d.Projects == nil {
		d.Projects = []models.Project{}
	}
	if d.Entries == nil {
		d.Entries = []models.Entry{}
	}
	if d.Settings.Year == 0 {
		d.Settings.Year = time.Now().Year()
	}
	if d.Settings.WeeklyTargetHours == 0 {
		d.Settings.WeeklyTargetHours = 40
	}
	if d.Settings.FederalState == "" {
		d.Settings.FederalState = "BY"
	}
	s.data = d
	return nil
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
	return d
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
