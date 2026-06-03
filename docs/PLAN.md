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
- `Settings`: year, federalState (z. B. "BY"), weeklyTargetHours
- `Project`: id, name, budgetHours, color, active
- `Entry`: date (YYYY-MM-DD), projectId, hours
- Feiertage werden zur Laufzeit berechnet (nicht persistiert)

## Projektstruktur
```
cmd/server/main.go                  # Bootstrap, HTTP-Server, Graceful Shutdown
internal/models/models.go           # Settings, Project, Entry, Data
internal/storage/store.go           # JSON load/save, RWMutex, atomic write
internal/holidays/holidays.go       # Wrapper um rickar/cal (DE-Regionen)
internal/forecast/agg.go            # Wochen-/Jahres-Aggregation, Burn-Down
internal/forecast/agg_test.go       # Unit-Tests der Aggregation
internal/web/handlers.go            # HTTP-Handler + Routing
internal/web/svg.go                 # Burn-Down-SVG-Generator
internal/web/util.go                # Formatierungs-Helfer
internal/web/templates/*.html       # Layout, Dashboard, Woche, Projekte, Settings
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
- **Wochenansicht**: Tabelle Projekte × Mo–Fr, Feiertage markiert, Eingabe pro Tag,
  Tages-/Wochensummen, Auslastung gegen Wochensoll, Wochennavigation.
- **Projekte**: CRUD, Budget, Farbe, aktiv/inaktiv, Restbudget + Burn-Down-SVG.
- **Einstellungen**: Jahr, Bundesland (Feiertage), Wochensollstunden.

## Verifikation
- `go vet ./...` und `go test ./...` ohne Fehler
- `docker build` erfolgreich; `docker compose up` → UI auf `:8080`
- Manuell: Projekt anlegen, Stunden eintragen, Feiertag sichtbar, Burn-Down korrekt
- GitHub-Actions-Lauf testet, baut und pusht Image (multi-arch) nach GHCR

## Mögliche Erweiterungen (später)
- Manuelle Sondertage (Urlaub/krank) zusätzlich zu automatischen Feiertagen
- Export (CSV) der Jahresdaten
- Mehrjahres-Unterstützung / Jahresarchiv
