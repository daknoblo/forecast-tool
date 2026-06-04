package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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

	store, err := storage.New(dataPath)
	if err != nil {
		log.Fatalf("storage init: %v", err)
	}

	srv, err := web.NewServer(store)
	if err != nil {
		log.Fatalf("server init: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("forecast-tool listening on %s (data: %s)", addr, dataPath)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
