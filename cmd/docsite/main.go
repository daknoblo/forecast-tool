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
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/docsite"
	"github.com/daknoblo/forecast-tool/internal/storage"
	"github.com/daknoblo/forecast-tool/internal/web"
)

func main() {
	out := flag.String("out", "site", "output directory for the generated site")
	repo := flag.String("repo", ".", "repository root holding README.md and docs/")
	script := flag.String("capture", filepath.Join("tools", "screenshots", "capture.mjs"), "Playwright capture script")
	withShots := flag.Bool("screenshots", true, "capture screenshots (needs node + playwright)")
	requireShots := flag.Bool("require-screenshots", false, "fail when the screenshots cannot be captured")
	language := flag.String("language", "de", "demo application language: de or en")
	flag.Parse()

	if err := run(*out, *repo, *script, *withShots, *requireShots, *language); err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
}

func run(out, repo, script string, withShots, requireShots bool, language string) error {
	if language != "de" && language != "en" {
		return fmt.Errorf("unsupported demo language %q; use de or en", language)
	}
	if requireShots && !withShots {
		return fmt.Errorf("-require-screenshots cannot be combined with -screenshots=false")
	}
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("output already exists: %s; choose a new directory", out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	restore, err := isolateDemo()
	if err != nil {
		return err
	}
	defer restore()

	staging, err := os.MkdirTemp(filepath.Dir(out), ".forecast-docsite-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	dataDir, err := os.MkdirTemp("", "forecast-demo-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dataDir) }()

	today := time.Now().UTC()
	if err := docsite.WriteDemoData(dataDir, today, language); err != nil {
		return fmt.Errorf("demo data: %w", err)
	}

	srv, baseURL, shutdown, err := startDemoServer(dataDir)
	if err != nil {
		return err
	}
	defer shutdown()
	_ = srv

	pages, err := docsite.DiscoverDemoPages(baseURL)
	if err != nil {
		return fmt.Errorf("discover navigation: %w", err)
	}
	shots := docsite.DemoShots(pages)

	fmt.Println("docsite: snapshotting the demo instance from", baseURL)
	if err := docsite.Snapshot(baseURL, filepath.Join(staging, "demo"), pages); err != nil {
		return err
	}

	if withShots {
		fmt.Println("docsite: capturing screenshots")
		if err := docsite.CaptureScreenshots(script, baseURL, filepath.Join(staging, "screenshots"), shots, language); err != nil {
			if requireShots {
				return err
			}
			fmt.Fprintln(os.Stderr, "docsite: skipping screenshots:", err)
			if err := os.RemoveAll(filepath.Join(staging, "screenshots")); err != nil {
				return err
			}
			shots = nil
		}
	} else {
		shots = nil
	}

	fmt.Println("docsite: rendering the documentation pages")
	if err := docsite.BuildSite(repo, staging, shots, pages, today.Format("2006-01-02")); err != nil {
		return err
	}
	if err := docsite.ValidateSite(staging, len(shots) > 0); err != nil {
		return fmt.Errorf("validate site: %w", err)
	}
	if err := os.Rename(staging, out); err != nil {
		return err
	}
	fmt.Println("docsite: wrote", out)
	return nil
}

// isolateDemo applies only inside the short-lived docsite process. Neither
// inherited credentials nor outbound HTTP connections belong in a public demo.
func isolateDemo() (func(), error) {
	saved := map[string]string{}
	for _, item := range os.Environ() {
		key, value, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "AZURE_") || strings.HasPrefix(key, "FORECAST_") || key == "DATA_DIR" {
			saved[key] = value
			if err := os.Unsetenv(key); err != nil {
				return nil, err
			}
		}
	}
	old := http.DefaultTransport
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil || !net.ParseIP(host).IsLoopback() {
				return nil, fmt.Errorf("demo blocks outbound connection to %s", address)
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
		},
	}
	http.DefaultTransport = transport
	return func() {
		transport.CloseIdleConnections()
		http.DefaultTransport = old
		for key, value := range saved {
			if err := os.Setenv(key, value); err != nil {
				fmt.Fprintln(os.Stderr, "docsite: restore environment:", err)
			}
		}
	}, nil
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
		Handler:           readOnlyDemo(srv.Handler()),
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

func readOnlyDemo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/export" {
			http.Error(w, "Static demo: operation disabled", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
