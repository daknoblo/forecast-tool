package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/daknoblo/forecast-tool/internal/logging"
	"github.com/daknoblo/forecast-tool/internal/storage"
	"github.com/daknoblo/forecast-tool/internal/web"
)

func main() {
	addr := envOr("FORECAST_ADDR", "")
	if addr == "" {
		if port := os.Getenv("PORT"); port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}
	dataDir := envOr("FORECAST_DATA_DIR", envOr("DATA_DIR", "appdata"))
	dataPath := filepath.Join(dataDir, "data.json")

	logger, logPath, closeLog := logging.Setup(dataDir)
	defer func() { _ = closeLog() }()
	slog.SetDefault(logger)
	// Route the standard library logger (used elsewhere) through slog so all
	// output ends up in both the container log and the rotating log file.
	log.SetFlags(0)
	log.SetOutput(slogWriter{logger})
	if logPath != "" {
		logger.Info("logging initialised", "file", logPath, "maxBytes", logging.DefaultMaxBytes, "maxBackups", logging.DefaultMaxBackups)
	}

	store, err := storage.New(dataPath)
	if err != nil {
		logger.Error("storage init failed", "error", err)
		os.Exit(1)
	}

	srv, err := web.NewServer(store, logger)
	if err != nil {
		logger.Error("server init failed", "error", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("forecast-tool listening", "addr", addr, "data", dataPath)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
}

// slogWriter adapts the standard library log output to slog at info level.
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	w.l.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
