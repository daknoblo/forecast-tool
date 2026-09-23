package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/daknoblo/forecast-tool/internal/ai"
	"github.com/daknoblo/forecast-tool/internal/forecast"
	"github.com/daknoblo/forecast-tool/internal/models"
)

const monthPreviewTTL = 30 * time.Minute

var errMonthPreviewStale = errors.New("Die Planungsdaten haben sich geändert oder die Vorschau ist abgelaufen. Bitte neu planen.")

type monthAIPreview struct {
	Context      forecast.MonthAIContext
	Plan         forecast.MonthAIPlan
	Revision     [32]byte
	Prompt       string
	SystemPrompt string
	Deployment   string
	Created      time.Time
}

type monthAIState struct {
	generating atomic.Bool
	mu         sync.Mutex
	previews   map[string]monthAIPreview
}

func monthPrompt(d models.Data) string {
	if value := strings.TrimSpace(d.Settings.MonthPlanningPrompt); value != "" {
		return value
	}
	return forecast.DefaultMonthPlanningPrompt
}

func monthSystemPrompt(d models.Data) string {
	if value := strings.TrimSpace(d.Settings.MonthPlanningSystemPrompt); value != "" {
		return value
	}
	return forecast.MonthPlanningSystemPrompt
}

func validateMonthPrompt(value, label string) error {
	if len([]rune(value)) > models.MaxMonthPlanningPrompt {
		return fmt.Errorf("Der %s darf höchstens %d Zeichen enthalten.", label, models.MaxMonthPlanningPrompt)
	}
	return nil
}

func (s *Server) monthSource(d models.Data, month, now time.Time) (forecast.MonthAIContext, [32]byte, error) {
	context, err := forecast.BuildMonthAIContext(d, s.calendar(d), month, now)
	if err != nil {
		return context, [32]byte{}, err
	}
	source := make([]models.Entry, 0)
	for _, entry := range d.Entries {
		if strings.HasPrefix(entry.Date, context.Month+"-") {
			source = append(source, entry)
		}
	}
	body, err := json.Marshal(struct {
		Context forecast.MonthAIContext
		Entries []models.Entry
	}{context, source})
	if err != nil {
		return context, [32]byte{}, fmt.Errorf("Die Planungsdaten konnten nicht aufbereitet werden.")
	}
	return context, sha256.Sum256(body), nil
}

func (s *Server) handleMonthPrompt(w http.ResponseWriter, r *http.Request) {
	if isPrivate(r) {
		http.Error(w, "Im privaten Modus kann der Planungsprompt nicht geändert werden.", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültiger Planungsprompt.", http.StatusBadRequest)
		return
	}
	prompt := strings.TrimSpace(r.PostForm.Get("prompt"))
	if err := validateMonthPrompt(prompt, "Planungsprompt"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	systemPrompt := strings.TrimSpace(r.PostForm.Get("systemPrompt"))
	if err := validateMonthPrompt(systemPrompt, "Systemprompt"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.Mutate(func(d *models.Data) error {
		d.Settings.MonthPlanningPrompt = prompt
		if r.PostForm.Has("systemPrompt") {
			d.Settings.MonthPlanningSystemPrompt = systemPrompt
		}
		return nil
	}); err != nil {
		s.logger.Error("month prompt save failed", "error", err)
		http.Error(w, "Die Planungsprompts konnten nicht gespeichert werden.", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMonthGenerate(w http.ResponseWriter, r *http.Request) {
	if isPrivate(r) {
		writeJSONError(w, http.StatusForbidden, "Im privaten Modus ist die KI-Planung gesperrt.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var in struct {
		Month        string  `json:"month"`
		Prompt       string  `json:"prompt"`
		SystemPrompt *string `json:"systemPrompt"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Ungültige Planungsanfrage.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "Die Anfrage muss genau ein JSON-Objekt enthalten.")
		return
	}
	month, err := time.Parse("2006-01", in.Month)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Ungültiger Monat (JJJJ-MM erwartet).")
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if err := validateMonthPrompt(in.Prompt, "Planungsprompt"); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.SystemPrompt != nil {
		*in.SystemPrompt = strings.TrimSpace(*in.SystemPrompt)
		if err := validateMonthPrompt(*in.SystemPrompt, "Systemprompt"); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if !s.monthAI.generating.CompareAndSwap(false, true) {
		writeJSONError(w, http.StatusConflict, "Eine KI-Planung läuft bereits. Bitte warte auf das Ergebnis.")
		return
	}
	defer s.monthAI.generating.Store(false)

	now := time.Now().UTC()
	d := s.store.Snapshot()
	context, revision, err := s.monthSource(d, month, now)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.aiConfig(r.Context(), d.Settings.AI)
	if err != nil {
		s.logger.Warn("month ai configuration unavailable", "error", err)
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var prompt, systemPrompt string
	if err := s.store.Mutate(func(d *models.Data) error {
		d.Settings.MonthPlanningPrompt = in.Prompt
		if in.SystemPrompt != nil {
			d.Settings.MonthPlanningSystemPrompt = *in.SystemPrompt
		}
		prompt, systemPrompt = monthPrompt(*d), monthSystemPrompt(*d)
		return nil
	}); err != nil {
		s.logger.Error("month prompt save failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Die Planungsprompts konnten nicht gespeichert werden.")
		return
	}
	message, err := json.Marshal(struct {
		Instructions string                  `json:"instructions"`
		Data         forecast.MonthAIContext `json:"data"`
	}{prompt, context})
	if err != nil {
		s.logger.Error("month context encoding failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Die Planungsdaten konnten nicht aufbereitet werden.")
		return
	}
	answer, err := ai.Ask(r.Context(), cfg, systemPrompt, string(message), s.logger)
	if err != nil {
		s.logger.Error("month ai request failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	proposal, err := forecast.DecodeMonthAIPlan(context, answer)
	if err != nil {
		s.logger.Warn("month ai response rejected", "error", err)
		writeJSONError(w, http.StatusBadGateway, "Die KI-Planung wurde verworfen: "+err.Error())
		return
	}
	_, current, err := s.monthSource(s.store.Snapshot(), month, time.Now().UTC())
	if err != nil || current != revision {
		writeJSONError(w, http.StatusConflict, errMonthPreviewStale.Error())
		return
	}
	token := rand.Text()
	s.monthAI.mu.Lock()
	if s.monthAI.previews == nil {
		s.monthAI.previews = make(map[string]monthAIPreview)
	}
	for key, preview := range s.monthAI.previews {
		if time.Since(preview.Created) > monthPreviewTTL {
			delete(s.monthAI.previews, key)
		}
	}
	if len(s.monthAI.previews) >= 8 {
		var oldest string
		for key, preview := range s.monthAI.previews {
			if oldest == "" || preview.Created.Before(s.monthAI.previews[oldest].Created) {
				oldest = key
			}
		}
		delete(s.monthAI.previews, oldest)
	}
	s.monthAI.previews[token] = monthAIPreview{
		Context: context, Plan: proposal, Revision: revision, Prompt: prompt, SystemPrompt: systemPrompt,
		Deployment: cfg.Deployment, Created: time.Now().UTC(),
	}
	s.monthAI.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"previewURL": "/month?month=" + url.QueryEscape(in.Month) + "&preview=" + url.QueryEscape(token),
	}); err != nil {
		s.logger.Error("month preview response failed", "error", err)
	}
}

func (s *Server) monthPreview(token string, d models.Data, month, now time.Time) (monthAIPreview, error) {
	s.monthAI.mu.Lock()
	preview, ok := s.monthAI.previews[token]
	s.monthAI.mu.Unlock()
	if !ok || now.Sub(preview.Created) > monthPreviewTTL || preview.Context.Month != month.Format("2006-01") {
		return monthAIPreview{}, errMonthPreviewStale
	}
	_, revision, err := s.monthSource(d, month, now)
	if err != nil || revision != preview.Revision {
		return monthAIPreview{}, errMonthPreviewStale
	}
	return preview, nil
}

func (s *Server) handleMonthSave(w http.ResponseWriter, r *http.Request) {
	if isPrivate(r) {
		writeJSONError(w, http.StatusForbidden, "Im privaten Modus kann kein Forecast gespeichert werden.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Ungültige Speicheranfrage.")
		return
	}
	token := r.PostForm.Get("preview")
	s.monthAI.mu.Lock()
	defer s.monthAI.mu.Unlock()
	preview, ok := s.monthAI.previews[token]
	now := time.Now().UTC()
	if !ok || now.Sub(preview.Created) > monthPreviewTTL {
		writeJSONError(w, http.StatusConflict, errMonthPreviewStale.Error())
		return
	}
	if len(preview.Plan.Unallocated) > 0 {
		writeJSONError(w, http.StatusConflict, "Nicht verteilbare Stunden verhindern das Speichern. Passe zuerst den Wochen-Forecast oder die Projektzeiträume an.")
		return
	}
	month, err := time.Parse("2006-01", preview.Context.Month)
	if err != nil {
		writeJSONError(w, http.StatusConflict, errMonthPreviewStale.Error())
		return
	}
	err = s.store.Mutate(func(d *models.Data) error {
		context, revision, err := s.monthSource(*d, month, now)
		if err != nil || revision != preview.Revision {
			return errMonthPreviewStale
		}
		if err := forecast.ApplyMonthAIPlan(d, context, preview.Plan); err != nil {
			return err
		}
		if d.SavedMonthPlans == nil {
			d.SavedMonthPlans = make(map[string]string)
		}
		d.SavedMonthPlans[preview.Context.Month] = now.Format(time.RFC3339)
		return nil
	})
	if err != nil {
		s.logger.Warn("month plan save failed", "error", err)
		if errors.Is(err, errMonthPreviewStale) {
			writeJSONError(w, http.StatusConflict, err.Error())
		} else {
			writeJSONError(w, http.StatusInternalServerError, "Der Forecast konnte nicht gespeichert werden. Die bisherigen Buchungen bleiben unverändert.")
		}
		return
	}
	delete(s.monthAI.previews, token)
	w.WriteHeader(http.StatusNoContent)
}
