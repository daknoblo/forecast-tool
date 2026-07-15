package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/daknoblo/forecast-tool/internal/logging"
	"github.com/daknoblo/forecast-tool/internal/storage"
	"github.com/daknoblo/forecast-tool/internal/web"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-healthcheck" || os.Args[1] == "healthcheck") {
		os.Exit(healthcheck())
	}

	addr := listenAddr()
	dataDir := envOr("FORECAST_DATA_DIR", envOr("DATA_DIR", "appdata"))
	dataPath := filepath.Join(dataDir, "data.json")

	logger, logPath, closeLog := logging.Setup(dataDir)
	defer func() { _ = closeLog() }()
	slog.SetDefault(logger)
	// Standard-Logger über slog leiten, damit Ausgaben im Container-Log und in
	// der rotierenden Logdatei landen.
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

// slogWriter leitet Standard-Logausgaben als Info-Einträge an slog weiter.
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

func listenAddr() string {
	addr := envOr("FORECAST_ADDR", "")
	if addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + strings.TrimPrefix(port, ":")
	}
	return ":8080"
}

func healthcheck() int {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + healthcheckPort() + "/healthz")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func healthcheckPort() string {
	addr := listenAddr()
	if strings.HasPrefix(addr, ":") {
		return strings.TrimPrefix(addr, ":")
	}
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	if strings.Count(addr, ":") == 0 {
		return addr
	}
	return "8080"
}
