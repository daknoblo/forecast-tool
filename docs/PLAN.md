# Plan: Forecast-Tool (Go) als Docker-Container

## Ziel
Web-basiertes Single-User Forecast-Tool in Go. Projekte mit Stundenbudget,
tagesweise (Mo–Fr) Stunden-Forecast pro Projekt, Wochen-/Jahres-Summen,
automatische Feiertage (Bundesland wählbar), konfigurierbare Wochensollzeit,
Auslastung in %, Restbudget + Burn-Down. Daten in JSON-Datei unter `appdata/`.
Build via GitHub Actions nach GHCR. Betrieb per `docker compose` auf anderem Host.

## Entscheidungen
- Stack: Go + `html/template` (Standardbibliothek, `net/http` ServeMux)
- Speicher: JSON-Datei in `/app/appdata/data.json`, persistiert via compose-Volume
- Auth: keine (Single-User, nur intern)
- Feiertage: automatisch via `github.com/rickar/cal/v2` (DE, Bundesland wählbar)
- Sollarbeitszeit: pro Woche konfigurierbar (Settings)
- Eingabe: pro Tag (Mo–Fr) pro Projekt; Wochensumme automatisch
- Budget-Sicht: Restbudget + Burn-Down-Diagramm (server-seitiges SVG, offline-fähig)
- Deployment: `docker-compose.yml`, Daten unter `appdata`-Volume

## Datenmodell (`data.json`)
- `Settings` (global): year (aktives Fiskaljahr), federalState (Default "SN"),
  weeklyTargetHours (Default 40), fiscalYearStartMonth, `ai{ endpoint, deployment, apiVersion }`
  (API-Key NICHT in der Datei – via Env `FORECAST_AI_API_KEY`)
- `FiscalYears` (pro FY): targetHours, vacationDaysH1/H2, standardTaskLabel, standardTaskHours.
  Defaults für neue/unkonfigurierte FY (`models.DefaultFYSettings`): Ziel 1440 h,
  Urlaub H1/H2 je 15 Tage, Standard Tasks 250 h.
- `Project`: id, name, budgetHours, color, active, fiscalYear
- `Entry`: date (YYYY-MM-DD), projectId, hours, kind (forecast | actual)
- Feiertage werden zur Laufzeit berechnet (nicht persistiert)

## Projektstruktur
```
cmd/server/main.go                  # Bootstrap, HTTP-Server, Graceful Shutdown
internal/models/models.go           # Settings, AISettings, Project, Entry, Data, Validate
internal/storage/store.go           # JSON load/save, RWMutex, atomic write, ReplaceJSON/ValidateJSON
internal/holidays/holidays.go       # Wrapper um rickar/cal (DE-Regionen)
internal/ai/client.go               # Azure-OpenAI-kompatibler KI-Client (JSON-Update per Prompt, Blueprint)
internal/logging/logging.go         # slog-Logger: stdout + rotierende Datei (10 MB) in appdata
internal/forecast/agg.go            # Wochen-/Jahres-Aggregation, Burn-Down
internal/forecast/agg_test.go       # Unit-Tests der Aggregation
internal/web/handlers.go            # HTTP-Handler + Routing
internal/web/svg.go                 # Burn-Down-SVG-Generator
internal/web/util.go                # Formatierungs-Helfer
internal/web/templates/*.html       # Layout, Dashboard, Woche, Projekte, Settings, JSON-Editor
internal/web/static/style.css       # Styles
appdata/.gitkeep                    # data.json zur Laufzeit (git-ignored)
docs/PLAN.md                        # dieser Plan
Dockerfile                          # Multi-stage, distroless
docker-compose.yml
.github/workflows/docker.yml        # CI: test + build + push -> GHCR
go.mod
```

## Funktionsumfang
- **Dashboard**: Jahres-Forecast gesamt, Projektanzahl, aktuelle KW, Budget-Tabelle
  (Budget/Verbraucht/Rest/Auslastung), Wochenauslastung.
- **Mehrwochen-Forecast**: Tabelle Projekte × Tage über mehrere Wochen, Feiertage markiert,
  Plan-/Ist-Eingabe pro Tag, Tages-/Wochensummen, Auslastung gegen Wochensoll, Navigation.
- **Projekte**: CRUD pro Fiskaljahr, Budget, Farbe, aktiv/inaktiv, Restbudget + Burn-Down-SVG.
- **Ziel/Kapazität**: FY-Ziel, Urlaub (pro Halbjahr), Standard Tasks, verfügbare Stunden,
  Soll je Woche/Monat/Quartal.
- **Einstellungen**: FY-Startmonat, Bundesland (Feiertage), Wochensollstunden, pro-FY-Werte,
  KI-Endpoint (Endpoint, Deployment, API-Version; API-Key aus Env). Konfigurationsdatei-
  Übersicht (Pfad/Größe) steht ganz unten.
- **Layout**: Zentraler App-Name über `web.AppName`/`{{appName}}` (Titel, Header-Brand, Footer).
  Navigation: Dashboard – Projekte – Forecast (`/week`) – Ziele (`/goal`) – JSON – Einstellungen.
  Footer mit Link auf das GitHub-Profil (Icon), ohne Wochensoll.
- **JSON-Editor** (`/data`): gesamte Datendatei im Browser bearbeiten, exportieren (`/export`)
  und mit serverseitiger Validierung speichern; optional per KI-Prompt aktualisieren (`/data/ai`).

## Verifikation
- `go vet ./...` und `go test ./...` ohne Fehler
- `docker build` erfolgreich; `docker compose up` → UI auf `:8080`
- Manuell: Projekt anlegen, Stunden eintragen, Feiertag sichtbar, Burn-Down korrekt
- GitHub-Actions-Lauf testet, baut und pusht Image (multi-arch) nach GHCR

## Mögliche Erweiterungen (später)
- Manuelle Sondertage (Urlaub/krank) zusätzlich zu automatischen Feiertagen
- Export (CSV) der Jahresdaten
- Mehrjahres-Unterstützung / Jahresarchiv
