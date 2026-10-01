package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputIsNeverDeleted(t *testing.T) {
	out := t.TempDir()
	sentinel := filepath.Join(out, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(out, "../..", "", false, false, "de"); err == nil {
		t.Fatal("existing output was accepted")
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "keep" {
		t.Fatalf("existing output changed: %q, %v", content, err)
	}
}

func TestBuildBothLanguagesWithoutPrivateData(t *testing.T) {
	for _, language := range []string{"de", "en"} {
		t.Run(language, func(t *testing.T) {
			t.Setenv("AZURE_RESOURCE_ID", "private-resource-do-not-publish")
			t.Setenv("FORECAST_AI_API_KEY", "private-key-do-not-publish")
			t.Setenv("FORECAST_DATA_DIR", filepath.Join(t.TempDir(), "must-not-exist"))
			out := filepath.Join(t.TempDir(), "site")
			if err := run(out, "../..", "", false, false, language); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"index.html", "docsite.html", "features.html", "demo/index.html", "demo/settings.html", "assets/site.css", ".nojekyll"} {
				content, err := os.ReadFile(filepath.Join(out, file))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(content), "private-resource-do-not-publish") || strings.Contains(string(content), "private-key-do-not-publish") {
					t.Errorf("private environment leaked into %s", file)
				}
			}
			content, err := os.ReadFile(filepath.Join(out, "demo/index.html"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), `<html lang="`+language+`">`) {
				t.Errorf("demo language does not match %s", language)
			}
			if os.Getenv("AZURE_RESOURCE_ID") != "private-resource-do-not-publish" {
				t.Error("environment was not restored")
			}
		})
	}
}

func TestDemoBlocksWritesAndExports(t *testing.T) {
	handler := readOnlyDemo(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, target := range []struct{ method, path string }{
		{"POST", "/goal/chat"}, {"POST", "/month/generate"}, {"POST", "/settings"},
		{"GET", "/export"}, {"GET", "/api/v1/data"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(target.method, target.path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", target.method, target.path, rec.Code)
		}
	}
}

func TestIsolationBlocksOutboundHTTP(t *testing.T) {
	restore, err := isolateDemo()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	resp, err := http.Get("http://example.com/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("outbound HTTP allowed")
	}
	if !strings.Contains(err.Error(), "demo blocks outbound") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidBuildOptions(t *testing.T) {
	for _, options := range []struct {
		language string
		required bool
	}{{"fr", false}, {"en", true}} {
		out := filepath.Join(t.TempDir(), "site")
		if err := run(out, "../..", "", false, options.required, options.language); err == nil {
			t.Fatal("invalid options accepted")
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("invalid build created output")
		}
	}
}

func TestFailedBuildCleansStagingAndDoesNotPublish(t *testing.T) {
	parent := t.TempDir()
	out := filepath.Join(parent, "site")
	if err := run(out, t.TempDir(), "", false, false, "en"); err == nil {
		t.Fatal("build with missing source documentation succeeded")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed build left output or staging: %v", entries)
	}
}
