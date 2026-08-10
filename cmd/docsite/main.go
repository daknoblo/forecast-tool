// Command docsite builds the public documentation site: it starts the
// application with a generated demo data set, renders every page into a static
// clickable snapshot, captures the screenshots and turns the repository's
// Markdown files into HTML. The result in -out is ready to be published to
// GitHub Pages.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/daknoblo/forecast-tool/internal/docsite"
	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
	"github.com/daknoblo/forecast-tool/internal/storage"
	"github.com/daknoblo/forecast-tool/internal/web"
)

func main() {
	out := flag.String("out", "site", "output directory for the generated site")
	repo := flag.String("repo", ".", "repository root holding README.md and docs/")
	script := flag.String("capture", filepath.Join("tools", "screenshots", "capture.mjs"), "Playwright capture script")
	withShots := flag.Bool("screenshots", true, "capture screenshots (needs node + playwright)")
	requireShots := flag.Bool("require-screenshots", false, "fail when the screenshots cannot be captured")
	flag.Parse()

	if err := run(*out, *repo, *script, *withShots, *requireShots); err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
}

func run(out, repo, script string, withShots, requireShots bool) error {
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}

	dataDir, err := os.MkdirTemp("", "forecast-demo-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dataDir) }()

	today := time.Now().UTC()
	if err := docsite.WriteDemoData(dataDir, today); err != nil {
		return fmt.Errorf("demo data: %w", err)
	}

	srv, baseURL, shutdown, err := startDemoServer(dataDir)
	if err != nil {
		return err
	}
	defer shutdown()
	_ = srv

	week := forecast.CurrentFYWeek(forecast.FiscalYearOf(today, models.DefaultFiscalYearStartMonth), models.DefaultFiscalYearStartMonth)
	pages := docsite.DemoPages(week)
	shots := docsite.DemoShots(week)

	fmt.Println("docsite: snapshotting the demo instance from", baseURL)
	if err := docsite.Snapshot(baseURL, filepath.Join(out, "demo"), pages); err != nil {
		return err
	}

	if withShots {
		fmt.Println("docsite: capturing screenshots")
		if err := docsite.CaptureScreenshots(script, baseURL, filepath.Join(out, "screenshots"), shots); err != nil {
			if requireShots {
				return err
			}
			fmt.Fprintln(os.Stderr, "docsite: skipping screenshots:", err)
			shots = nil
		}
	} else {
		shots = nil
	}

	fmt.Println("docsite: rendering the documentation pages")
	if err := docsite.BuildSite(repo, out, shots, pages, today.Format("02.01.2006")); err != nil {
		return err
	}
	fmt.Println("docsite: wrote", out)
	return nil
}

// startDemoServer runs the real application against the demo document on a
// loopback port, so the snapshot and the screenshots show exactly what a user
// would see.
func startDemoServer(dataDir string) (*http.Server, string, func(), error) {
	store, err := storage.New(filepath.Join(dataDir, "data.json"))
	if err != nil {
		return nil, "", nil, err
	}
	// The demo server is short-lived and its log output would only drown the
	// build output.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := web.NewServer(store, logger)
	if err != nil {
		return nil, "", nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "docsite: demo server:", err)
		}
	}()

	baseURL := "http://" + ln.Addr().String()
	if err := waitReady(baseURL + "/healthz"); err != nil {
		_ = httpSrv.Close()
		return nil, "", nil, err
	}
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}
	return httpSrv, baseURL, shutdown, nil
}

func waitReady(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:gosec // loopback address chosen by this process
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("status %s", resp.Status)
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("demo server did not become ready: %w", last)
}
