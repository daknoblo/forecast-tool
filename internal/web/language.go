package web

import (
	"context"
	"net/http"

	"github.com/daknoblo/forecast-tool/internal/i18n"
)

type languageContextKey struct{}

func (s *Server) requestLanguage(r *http.Request) string {
	if language, ok := r.Context().Value(languageContextKey{}).(string); ok {
		return language
	}
	return i18n.Language(s.store.Snapshot().Settings.Language)
}

func (s *Server) withLanguage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		language := i18n.Language(s.store.Snapshot().Settings.Language)
		w.Header().Set("Content-Language", language)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), languageContextKey{}, language)))
	})
}

func (s *Server) translate(r *http.Request, message string) string {
	return i18n.Text(s.requestLanguage(r), message)
}

func (s *Server) localizedJSONError(w http.ResponseWriter, r *http.Request, status int, message string) {
	w.Header().Set("Content-Language", s.requestLanguage(r))
	writeJSONError(w, status, s.translate(r, message))
}

func localizedChatPresets(language string) []ChatPreset {
	presets := make([]ChatPreset, len(chatPresets))
	for index, preset := range chatPresets {
		presets[index] = ChatPreset{
			Label: i18n.Text(language, preset.Label), Prompt: i18n.Text(language, preset.Prompt),
		}
	}
	return presets
}
