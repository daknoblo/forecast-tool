// Package api exposes a small JSON/REST API under /api/v1 so the forecast can be
// read and synchronized from an external tool (e.g. a desktop client). It is
// protected by two bearer tokens supplied via environment variables: a
// read-only token and a read+write token. The HTML UI is intentionally left
// unauthenticated; only this API sub-tree is guarded.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/storage"
)

// ReadTokenEnv and WriteTokenEnv name the environment variables that supply the
// API bearer tokens. The read token grants read-only (GET) access; the write
// token grants full read+write access. The tokens are never stored in the data
// file and never logged.
const (
	ReadTokenEnv  = "FORECAST_API_READ_TOKEN"  // #nosec G101 -- env var name, not a credential
	WriteTokenEnv = "FORECAST_API_WRITE_TOKEN" // #nosec G101 -- env var name, not a credential
)

// maxBodyBytes bounds the size of a request body the API will read, protecting
// against accidental or malicious oversized payloads.
const maxBodyBytes = 2 << 20 // 2 MiB

// scope describes the access level a request's token grants.
type scope int

const (
	scopeNone scope = iota
	scopeRead
	scopeWrite
)

// Server holds the dependencies shared by the API handlers.
type Server struct {
	store      *storage.Store
	logger     *slog.Logger
	readToken  string
	writeToken string
}

// New builds the JSON API handler mounted under /api/. It reads the bearer
// tokens from the environment once at construction time. When neither token is
// configured the whole API is disabled (every request answered with 503), so a
// write-capable endpoint is never exposed by accident.
func New(store *storage.Store, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		store:      store,
		logger:     logger,
		readToken:  strings.TrimSpace(os.Getenv(ReadTokenEnv)),
		writeToken: strings.TrimSpace(os.Getenv(WriteTokenEnv)),
	}
	if s.readToken == "" && s.writeToken == "" {
		logger.Warn("api disabled: no tokens configured", "readEnv", ReadTokenEnv, "writeEnv", WriteTokenEnv)
	} else {
		logger.Info("api enabled", "read", s.readToken != "", "write", s.writeToken != "")
	}
	return s.routes()
}

// routes builds the API mux and wraps it in the auth middleware. Patterns carry
// the full /api/v1 prefix because the parent mux mounts this handler on the
// "/api/" subtree without stripping the path.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Read endpoints (need a read or write token).
	mux.HandleFunc("GET /api/v1/data", s.handleGetData)
	mux.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	mux.HandleFunc("GET /api/v1/projects/summary", s.handleProjectsSummary)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.handleGetProject)
	mux.HandleFunc("GET /api/v1/entries", s.handleListEntries)
	mux.HandleFunc("GET /api/v1/goal", s.handleGetGoal)

	// Write endpoints (need the write token).
	mux.HandleFunc("POST /api/v1/entries/sync", s.handleSyncEntries)
	mux.HandleFunc("POST /api/v1/projects", s.handleCreateProject)
	mux.HandleFunc("PUT /api/v1/projects/{id}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /api/v1/projects/{id}", s.handleDeleteProject)
	mux.HandleFunc("PUT /api/v1/settings", s.handleUpdateSettings)
	mux.HandleFunc("PUT /api/v1/settings/fiscal-years/{year}", s.handleUpdateFYSettings)

	return s.authMiddleware(mux)
}

// authMiddleware enforces bearer-token authentication and method-based scoping
// on every API request. GET/HEAD need a read (or write) token; any mutating
// method needs the write token. It also logs the request outcome, never
// exposing the token value.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Fail closed when the API was never configured with any token.
		if s.readToken == "" && s.writeToken == "" {
			s.writeError(w, http.StatusServiceUnavailable, "API deaktiviert: keine Tokens konfiguriert")
			s.logRequest(r, http.StatusServiceUnavailable, start)
			return
		}
		granted := s.classify(bearerToken(r))
		if granted == scopeNone {
			w.Header().Set("WWW-Authenticate", "Bearer")
			s.writeError(w, http.StatusUnauthorized, "ungültiger oder fehlender API-Token")
			s.logRequest(r, http.StatusUnauthorized, start)
			return
		}
		if requiredScope(r.Method) == scopeWrite && granted != scopeWrite {
			s.writeError(w, http.StatusForbidden, "dieser Token erlaubt nur Lesezugriff")
			s.logRequest(r, http.StatusForbidden, start)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logRequest(r, rec.status, start)
	})
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. It returns "" when the header is missing or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// classify maps a presented token to the access level it grants using a
// constant-time comparison so token values cannot be probed via timing.
func (s *Server) classify(token string) scope {
	if token == "" {
		return scopeNone
	}
	if s.writeToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.writeToken)) == 1 {
		return scopeWrite
	}
	if s.readToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.readToken)) == 1 {
		return scopeRead
	}
	return scopeNone
}

// requiredScope returns the minimum scope a request method needs: read for safe
// methods (GET/HEAD), write for anything that mutates state.
func requiredScope(method string) scope {
	switch method {
	case http.MethodGet, http.MethodHead:
		return scopeRead
	default:
		return scopeWrite
	}
}

// statusRecorder captures the response status code for request logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// sanitizeLog strips characters that could forge log lines (log injection).
var sanitizeLog = strings.NewReplacer("\n", "", "\r", "", "\t", " ")

// logRequest records the outcome of an API request. The token value is never
// logged; user-controlled fields are sanitized to prevent log injection.
func (s *Server) logRequest(r *http.Request, status int, start time.Time) {
	s.logger.Info("api request",
		"method", sanitizeLog.Replace(r.Method),
		"path", sanitizeLog.Replace(r.URL.Path),
		"status", status,
		"remoteAddr", r.RemoteAddr,
		"durationMs", time.Since(start).Milliseconds(),
	)
}

// writeJSON serializes v as JSON with the given status code.
func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("api response encode failed", "error", err)
	}
}

// writeError sends a JSON error body {"error": msg} with the given status.
func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON strictly decodes the request body into dst, rejecting unknown
// fields and trailing data so client typos surface as errors.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("ungültiger JSON-Body: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("ungültiger JSON-Body: zusätzliche Daten nach dem JSON-Objekt")
	}
	return nil
}
