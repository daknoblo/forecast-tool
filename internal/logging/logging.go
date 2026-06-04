// Package logging provides a small, dependency-free logger that writes to both
// the container's standard output (so logs are visible via `docker logs`) and a
// size-capped, self-rotating file inside the application's data directory.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// DefaultMaxBytes is the size threshold (10 MB) at which the log file rotates.
const DefaultMaxBytes int64 = 10 << 20

// DefaultMaxBackups is how many rotated files are kept (forecast.log.1..N).
const DefaultMaxBackups = 3

// rotatingWriter is an io.Writer that appends to a file and, once the file
// exceeds maxBytes, rotates it (keeping a bounded number of backups). It is
// safe for concurrent use.
type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

func newRotatingWriter(path string, maxBytes int64, maxBackups int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	w := &rotatingWriter{path: path, maxBytes: maxBytes, maxBackups: maxBackups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}

// Write appends p to the current log file, rotating first if the file would
// exceed the configured size threshold.
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			// Keep logging to the existing file rather than dropping the line.
			fmt.Fprintf(os.Stderr, "logging: rotation failed: %v\n", err)
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file, shifts the backup chain
// (forecast.log.2 -> .3, .1 -> .2, current -> .1) and opens a fresh file.
func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	// Drop the oldest backup, then shift the rest up by one.
	oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
	_ = os.Remove(oldest)
	for i := w.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.path, i)
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Rename(src, dst)
	}
	if w.maxBackups > 0 {
		_ = os.Rename(w.path, w.path+".1")
	} else {
		_ = os.Remove(w.path)
	}
	return w.open()
}

// Close releases the underlying file handle.
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Setup creates a slog.Logger that writes to both os.Stdout (container output)
// and a rotating log file at dataDir/forecast.log (10 MB cap, 3 backups). It
// returns the logger, the resolved log-file path and a close function that the
// caller should defer. If the log file cannot be opened, logging falls back to
// stdout only and no error is returned.
func Setup(dataDir string) (*slog.Logger, string, func() error) {
	path := filepath.Join(dataDir, "forecast.log")
	rw, err := newRotatingWriter(path, DefaultMaxBytes, DefaultMaxBackups)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging: file logging disabled (%v)\n", err)
		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
		return logger, "", func() error { return nil }
	}
	mw := io.MultiWriter(os.Stdout, rw)
	logger := slog.New(slog.NewTextHandler(mw, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return logger, path, rw.Close
}
