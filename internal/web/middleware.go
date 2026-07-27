package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

// contentSecurityPolicy locks the page down to same-origin resources. Inline
// scripts and styles are still allowed because the templates use inline
// <script> blocks, inline event handlers and style attributes; everything else
// (external scripts, plugins, framing, cross-origin form posts) is denied.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'"

// securityHeaders adds hardening response headers to every reply. They are
// cheap, static and independent of the reverse proxy in front of the app, so
// the protection holds no matter how the app is deployed.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), interest-cohort=()")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// requireSameOrigin rejects state-changing requests that a browser reports as
// cross-site. The HTML UI has no authentication, so without this check any web
// page a user visits could silently post to the app (CSRF) — including the
// destructive reset endpoint.
//
// Non-browser clients (curl, scripts) send neither Sec-Fetch-Site nor Origin
// and are therefore still allowed; the check only removes the browser-driven
// attack path, which is exactly the one that exists here.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || sameOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
	})
}

// isSafeMethod reports whether the method is read-only and therefore exempt
// from the same-origin requirement.
func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin reports whether the request looks like it was initiated by this
// site. It prefers the Fetch metadata header and falls back to comparing the
// Origin header's host with the host the request was addressed to.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// No Fetch metadata and no Origin: not a browser-initiated request.
		return true
	}
	if origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// isSecureRequest reports whether the client is talking HTTPS, either directly
// or through a TLS-terminating reverse proxy. It decides whether cookies may
// carry the Secure attribute.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// cacheForever marks the embedded static assets as immutable. They are served
// with a content hash in the query string (see assetURL), so a new build always
// produces a new URL and a stale cache entry can never be used.
func cacheForever(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// assetVersion is a short content hash over all embedded static files. It is
// computed once at startup and appended to static URLs for cache busting.
var assetVersion = hashStatic()

// assetURL appends the build's asset version to a static path.
func assetURL(path string) string {
	return path + "?v=" + assetVersion
}

// hashStatic hashes the embedded static files (names and contents) so any
// change to them yields a new asset version.
func hashStatic() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, rerr := staticFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}
