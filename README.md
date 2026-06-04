# forecast-tool

Ein leichtgewichtiges Single-User **Forecast-Tool** in Go mit Web-Oberfläche.
Lege Projekte mit Stundenbudget an, forecaste tageweise (Mo–Fr) Stunden pro
Projekt und behalte Wochen-/Jahressummen, Auslastung und Restbudget im Blick.
Die Planung erfolgt **fiskaljahr-basiert** (frei wählbarer Startmonat).
Feiertage werden je nach Bundesland automatisch berücksichtigt.

Die Daten werden in einer einfachen JSON-Datei unter `appdata/data.json`
gespeichert – keine Datenbank nötig. Die Datei lässt sich im Browser direkt
bearbeiten, exportieren und optional per KI-Prompt aktualisieren.

## Features
- Projekte mit Stundenbudget (CRUD, Farbe, aktiv/inaktiv), pro Fiskaljahr
- Mehrwochen-Forecast-Ansicht: Projekte × Tage (Mo–Fr) über mehrere Wochen,
  Eingabe von Plan- und Ist-Stunden pro Tag, automatische Summen (Summenspalte
  zentriert), Buttons zum Leeren einzelner Tage oder ganzer Wochen
- Konfigurierbare **Auslastungs-Ampel**: vier Status (Burnrate Minimum, Optimal,
  Zu hoch, Überbucht) mit frei wählbaren Schwellen (Std.) und Labels; farbige
  Punkte in der Forecast-Übersicht sowie in den Wochentabellen von Dashboard und Zielen
- Fiskaljahr-Logik (frei wählbarer Startmonat) mit zentralem FY-Umschalter im Header
- Automatische Feiertage (alle 16 Bundesländer wählbar)
- Konfigurierbare Wochensollstunden, Urlaub (pro Halbjahr), Standard Tasks
- Ziel-/Kapazitätsrechnung: verfügbare Stunden, Auslastung in %, Soll je Woche/Monat/Quartal
- Restbudget je Projekt + Burn-Down-Diagramm (server-seitiges SVG)
- Dashboard mit Jahresübersicht und Wochenauslastung
- **JSON-Editor** im Browser: gesamte Datendatei bearbeiten, exportieren und mit
  serverseitiger Validierung speichern
- **KI-Aktualisierung**: per Prompt das JSON über einen konfigurierbaren, Azure-OpenAI-
  kompatiblen Endpoint (z. B. Azure AI Foundry Model-Router) aktualisieren lassen –
  das Ergebnis wird vor dem Speichern geprüft
- Daten als JSON im Volume, läuft als schlanker Container

### Standardwerte für neue Daten
Beim ersten Start (leere Datendatei) bzw. für noch nicht konfigurierte Fiskaljahre
gelten folgende Vorgaben:

| Einstellung            | Default        |
|------------------------|----------------|
| Bundesland (Feiertage) | `SN` (Sachsen) |
| Wochensollstunden      | `40`           |
| FY-Ziel                | `1440` h       |
| Urlaub H1 / H2         | `15` / `15` Tage |
| Standard Tasks         | `250` h        |

## Schnellstart (lokal mit Go)
```bash
go run ./cmd/server
# UI: http://localhost:8080
```

Konfiguration über Umgebungsvariablen:

| Variable             | Default     | Beschreibung                          |
|----------------------|-------------|---------------------------------------|
| `FORECAST_ADDR`      | `:8080`     | Listen-Adresse                        |
| `FORECAST_DATA_DIR`  | `appdata`   | Verzeichnis für `data.json`           |
| `PORT`               | `8080`      | Port (Alias, falls `FORECAST_ADDR` nicht gesetzt) |
| `DATA_DIR`           | `appdata`   | Daten-Verzeichnis (Alias für `FORECAST_DATA_DIR`) |
| `FORECAST_AI_API_KEY`| –           | Secret-API-Key für den KI-Endpoint (nicht in `data.json` gespeichert) |

## JSON-Editor & KI-Aktualisierung
Unter dem Menüpunkt **JSON** (`/data`) lässt sich die komplette Datendatei direkt
im Browser bearbeiten. Beim Speichern wird der Inhalt streng validiert (gültiges
JSON, bekannte Felder, vorhandene Projekt-Referenzen usw.); ungültige Eingaben
werden abgelehnt, ohne die gespeicherten Daten zu verändern. Über denselben
Bereich kann die Datei als JSON exportiert/heruntergeladen werden.

Optional kann das Dokument per **KI-Prompt** aktualisiert werden (z. B. „Erstelle
ein Projekt namens ABC im Fiskaljahr 2027"). Dazu in den **Einstellungen** unter
*KI-Endpoint* einen Azure-OpenAI-kompatiblen Endpoint hinterlegen:

| Feld         | Beispiel                                  |
|--------------|-------------------------------------------|
| Endpoint-URL | `https://mein-resource.openai.azure.com`  |
| Deployment   | `model-router`                            |
| API-Version  | `2024-10-21`                              |

Der **API-Key** wird **nicht** in `data.json` gespeichert, sondern über die
Umgebungsvariable `FORECAST_AI_API_KEY` bereitgestellt – z. B. als Docker-Secret
bzw. `environment`-Eintrag in `docker-compose.yml`. Lokal genügt:

```bash
FORECAST_AI_API_KEY=dein-key go run ./cmd/server
```

Für Docker Compose lege eine `.env` neben `docker-compose.yml` an (Vorlage:
`.env.example`, git-ignored):

```bash
cp .env.example .env
# .env editieren und FORECAST_AI_API_KEY=... setzen
docker compose up -d
```

Prompt und aktuelles JSON werden an den Endpoint gesendet; das Ergebnis wird in
den Editor eingefügt und geprüft. Gespeichert wird erst nach explizitem Klick auf
*Speichern*. Dem Modell wird zusätzlich ein **Blueprint** (vollständiges
Beispiel-Dokument) mitgesendet, damit es das exakte JSON-Format kennt.

Für regelmäßige, über ein ganzes Fiskaljahr gleichmäßig verteilte Forecasts gibt die
KI keine hunderten Tageseinträge aus (das würde das Token-Limit sprengen), sondern
eine kompakte Direktive `forecastPlan` (`projectId`, `fiscalYear`, `hoursPerWeek`,
`kind`). Der Server expandiert diese automatisch in Mo–Fr-Einträge
(`hoursPerWeek/5` pro Werktag) für das gesamte Fiskaljahr.

## Logging
Die Anwendung schreibt Logs **gleichzeitig** in den Container-Output (`docker logs`)
und in eine Datei `appdata/forecast.log`. Die Datei rotiert automatisch bei **10 MB**
(bis zu drei Backups `forecast.log.1`–`forecast.log.3`); ein externes Paket ist nicht
nötig. KI-Aufrufe werden mit Endpoint/Deployment, Prompt-/Antwortgröße, `finish_reason`,
Token-Verbrauch und Dauer protokolliert (der **API-Key wird nie geloggt**), was das
Debugging des KI-Endpoints erleichtert. Fehler und Warnungen erscheinen zusätzlich im
Container-Output.

## Mit Docker bauen und starten
```bash
docker build -t forecast-tool .
docker run -p 8080:8080 -v "$PWD/appdata:/app/appdata" forecast-tool
```

## Mit docker compose (Ziel-Host)
`docker-compose.yml` zieht standardmäßig das fertige Image aus der GitHub
Container Registry und persistiert die Daten unter `./appdata`:
```bash
docker compose up -d
```
Möchtest du lokal bauen statt zu ziehen, kommentiere in `docker-compose.yml`
die `image:`-Zeile aus und aktiviere `build: .`.

## CI/CD: Container über GitHub Actions nach GHCR
Der Workflow [.github/workflows/docker.yml](.github/workflows/docker.yml) führt
bei jedem Push auf `main` (und bei Tags `v*`) Folgendes aus:
1. `go vet` + `go test`
2. Multi-Arch-Build (`linux/amd64`, `linux/arm64`)
3. Push nach `ghcr.io/<owner>/forecast-tool`

Das Image wird mit `latest` (auf `main`), der Git-SHA und – bei Tags – mit der
Version getaggt.

### Was du einmalig einrichten musst
1. **Repository-Berechtigungen**: In *Settings → Actions → General → Workflow
   permissions* „Read and write permissions" aktivieren (oder es ist via der
   `permissions:`-Angabe im Workflow bereits abgedeckt). Es ist **kein** PAT
   nötig – der Workflow nutzt das automatische `GITHUB_TOKEN`.
2. **Erster Push** erzeugt das Package `forecast-tool` (zunächst **privat**).
   Unter *GitHub → dein Profil → Packages → forecast-tool → Package settings*
   kannst du:
   - die Sichtbarkeit auf **public** stellen (dann ist kein Login zum Pullen nötig), oder
   - es **privat** lassen.

### Auf dem Ziel-Host pullen
- **Public Image**: einfach `docker compose up -d` – kein Login.
- **Privates Image**: einmalig anmelden mit einem Personal Access Token
  (classic) mit Scope `read:packages`:
  ```bash
  echo "<DEIN_PAT>" | docker login ghcr.io -u <dein-github-user> --password-stdin
  docker compose up -d
  ```

> Hinweis: Der Owner-Teil des Image-Namens muss kleingeschrieben sein, z. B.
> `ghcr.io/daknoblo/forecast-tool`. Passe die `image:`-Zeile in
> `docker-compose.yml` an deinen GitHub-Benutzer/-Organisation an.

## Tests
```bash
go test ./...
```

## Projektstruktur & Plan
Siehe [docs/PLAN.md](docs/PLAN.md) für Architektur und Designentscheidungen.