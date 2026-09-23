package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/daknoblo/forecast-tool/internal/ai"
	"github.com/daknoblo/forecast-tool/internal/foundry"
	"github.com/daknoblo/forecast-tool/internal/models"
)

const (
	foundryResourceEnv = "AZURE_RESOURCE_ID"
	foundryTenantEnv   = "AZURE_TENANT_ID"
	foundryClientEnv   = "AZURE_CLIENT_ID"
	foundrySecretEnv   = "AZURE_CLIENT_SECRET" // #nosec G101 -- environment variable name
)

type foundrySource interface {
	Refresh(context.Context) (foundry.Snapshot, error)
	Authorize(*http.Request) error
}

type foundryState struct {
	enabled                        bool
	secretSet                      bool
	resourceID, tenantID, clientID string
	setupErr                       error
	source                         foundrySource
	gate                           chan struct{}
	snapshot                       foundry.Snapshot
	lastAttempt                    time.Time
	refreshErr                     error
}

func newFoundryState() *foundryState {
	identity := foundry.Identity{
		ResourceID: trim(os.Getenv(foundryResourceEnv)), TenantID: trim(os.Getenv(foundryTenantEnv)),
		ClientID: trim(os.Getenv(foundryClientEnv)), ClientSecret: os.Getenv(foundrySecretEnv),
	}
	state := &foundryState{
		enabled:    identity.ResourceID != "" || identity.TenantID != "" || identity.ClientID != "" || identity.ClientSecret != "",
		secretSet:  identity.ClientSecret != "",
		resourceID: identity.ResourceID, tenantID: identity.TenantID, clientID: identity.ClientID,
		gate: make(chan struct{}, 1),
	}
	if state.enabled {
		state.source, state.setupErr = foundry.New(identity)
	}
	return state
}

func (f *foundryState) catalog(ctx context.Context, force bool) (foundry.Snapshot, error) {
	if f.setupErr != nil {
		return foundry.Snapshot{}, f.setupErr
	}
	if f.source == nil {
		return foundry.Snapshot{}, fmt.Errorf("Foundry-Zugangsdaten sind nicht konfiguriert")
	}
	select {
	case f.gate <- struct{}{}:
		defer func() { <-f.gate }()
	case <-ctx.Done():
		return foundry.Snapshot{}, fmt.Errorf("Laden der Foundry-Deployments wurde abgebrochen")
	}
	ttl := 5 * time.Minute
	if f.refreshErr != nil {
		ttl = 15 * time.Second
	}
	if !force && !f.lastAttempt.IsZero() && time.Since(f.lastAttempt) < ttl {
		return f.snapshot, f.refreshErr
	}
	f.snapshot, f.refreshErr = f.source.Refresh(ctx)
	f.lastAttempt = time.Now()
	if f.refreshErr != nil {
		f.snapshot = foundry.Snapshot{}
	}
	return f.snapshot, f.refreshErr
}

type foundryView struct {
	Enabled, SelectedPresent, SecretSet      bool
	ResourceID, TenantID, ClientID, Endpoint string
	Error, RefreshedAt                       string
	Deployments                              []foundry.Deployment
}

func (s *Server) foundrySettings(r *http.Request, selected string) foundryView {
	if s.foundry == nil || !s.foundry.enabled {
		return foundryView{}
	}
	view := foundryView{Enabled: true}
	if isPrivate(r) {
		return view
	}
	view.ResourceID, view.TenantID, view.ClientID = s.foundry.resourceID, s.foundry.tenantID, s.foundry.clientID
	view.SecretSet = s.foundry.secretSet
	catalog, err := s.foundry.catalog(r.Context(), false)
	if err != nil {
		s.logger.Warn("foundry discovery failed", "error", err)
		view.Error = err.Error()
		return view
	}
	view.Endpoint = catalog.Endpoint
	view.RefreshedAt = catalog.RefreshedAt.Format("02.01.2006 15:04 UTC")
	for _, deployment := range catalog.Deployments {
		if deployment.SupportsChat() {
			view.Deployments = append(view.Deployments, deployment)
			if deployment.Name == selected {
				view.SelectedPresent = true
			}
		}
	}
	return view
}

func (s *Server) aiReady(settings models.AISettings) bool {
	if s.foundry != nil && s.foundry.enabled {
		return s.foundry.setupErr == nil && s.foundry.source != nil && trim(settings.Deployment) != ""
	}
	return aiConfigured(effectiveAI(settings))
}

func (s *Server) aiConfig(ctx context.Context, settings models.AISettings) (ai.Config, error) {
	if s.foundry != nil && s.foundry.enabled {
		catalog, err := s.foundry.catalog(ctx, false)
		if err != nil {
			return ai.Config{}, err
		}
		deployment, exists := catalog.Find(settings.Deployment)
		if !exists || !deployment.SupportsChat() {
			return ai.Config{}, fmt.Errorf("Wähle in den Einstellungen ein verfügbares Foundry-Chat-Deployment aus")
		}
		return ai.Config{
			Endpoint: catalog.Endpoint, Deployment: deployment.Name, Reasoning: deployment.Reasoning,
			Authorize: s.foundry.source.Authorize,
		}, nil
	}
	cfg := effectiveAI(settings)
	if !aiConfigured(cfg) {
		return ai.Config{}, fmt.Errorf("Konfiguriere den KI-Endpoint und das Deployment in den Einstellungen sowie die Zugangsdaten per Umgebungsvariablen")
	}
	return ai.Config{Endpoint: cfg.Endpoint, Deployment: cfg.Deployment, APIVersion: cfg.APIVersion, APIKey: cfg.APIKey}, nil
}

func (s *Server) handleFoundryRefresh(w http.ResponseWriter, r *http.Request) {
	if isPrivate(r) {
		http.Error(w, "Im privaten Modus ist das Laden der Foundry-Deployments gesperrt.", http.StatusForbidden)
		return
	}
	if s.foundry == nil || !s.foundry.enabled {
		http.Error(w, "Foundry ist nicht konfiguriert.", http.StatusBadRequest)
		return
	}
	if _, err := s.foundry.catalog(r.Context(), true); err != nil {
		s.logger.Warn("foundry refresh failed", "error", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/settings#ai", http.StatusSeeOther)
}

func (s *Server) handleAISettingsSave(w http.ResponseWriter, r *http.Request) {
	if isPrivate(r) {
		http.Error(w, "Im privaten Modus sind Änderungen an der KI-Anbindung gesperrt.", http.StatusForbidden)
		return
	}
	deployment := trim(r.FormValue("aiDeployment"))
	if len(deployment) > 100 || strings.ContainsAny(deployment, "/\\?#%") || deployment == "." || deployment == ".." {
		http.Error(w, "Ungültiger KI-Deployment-Name.", http.StatusBadRequest)
		return
	}
	identity := s.foundry != nil && s.foundry.enabled
	endpoint, version := trim(r.FormValue("aiEndpoint")), trim(r.FormValue("aiApiVersion"))
	if identity {
		if deployment != "" {
			catalog, err := s.foundry.catalog(r.Context(), false)
			if err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			selected, ok := catalog.Find(deployment)
			if !ok || !selected.SupportsChat() {
				http.Error(w, "Das ausgewählte Foundry-Deployment ist nicht für Chat verfügbar.", http.StatusBadRequest)
				return
			}
		}
	} else {
		if len(endpoint) > 4096 || len(version) > 100 {
			http.Error(w, "KI-Endpoint oder API-Version ist zu lang.", http.StatusBadRequest)
			return
		}
		if endpoint != "" {
			probe := deployment
			if probe == "" {
				probe = "deployment"
			}
			if _, _, err := ai.ChatURL(endpoint, probe, version); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	if err := s.store.Mutate(func(d *models.Data) error {
		d.Settings.AI.Deployment = deployment
		d.Settings.AI.APIKey = ""
		if !identity {
			d.Settings.AI.Endpoint, d.Settings.AI.APIVersion = endpoint, version
		}
		return nil
	}); err != nil {
		s.logger.Error("ai settings save failed", "error", err)
		http.Error(w, "KI-Einstellungen konnten nicht gespeichert werden.", http.StatusInternalServerError)
		return
	}
	s.settingsSaved(w, r)
}
