package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daknoblo/forecast-tool/internal/storage"
)

// newTestHandler builds a server backed by a throwaway data file.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	srv, err := NewServer(store, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.Handler()
}

func TestSecurityHeadersArePresent(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", csp)
	}
}

func TestCrossSitePostIsRejected(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			name:    "cross-site fetch metadata",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			want:    http.StatusForbidden,
		},
		{
			name:    "foreign origin",
			headers: map[string]string{"Origin": "https://evil.example"},
			want:    http.StatusForbidden,
		},
		{
			name:    "same-origin fetch metadata",
			headers: map[string]string{"Sec-Fetch-Site": "same-origin"},
			want:    http.StatusSeeOther,
		},
		{
			name:    "matching origin",
			headers: map[string]string{"Origin": "http://example.com"},
			want:    http.StatusSeeOther,
		},
		{
			// Non-browser clients send neither header and stay allowed.
			name:    "no browser headers",
			headers: nil,
			want:    http.StatusSeeOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://example.com/private", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestStaticAssetsAreVersionedAndCacheable(t *testing.T) {
	h := newTestHandler(t)

	url := assetURL("/static/style.css")
	if !strings.Contains(url, "?v=") {
		t.Fatalf("assetURL = %q, want a version query", url)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want an immutable directive", cc)
	}
}

func TestPrivateModeMasksFigures(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: privateCookie, Value: "1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), maskedValue) {
		t.Error("private dashboard does not contain any masked value")
	}
}
