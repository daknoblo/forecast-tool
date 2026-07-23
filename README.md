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
- Projekte mit Stundenbudget (CRUD, Farbe, aktiv/inaktiv), pro Fiskaljahr, mit
  eindeutiger **TaskID** (Pflichtfeld beim Anlegen, z. B. `C.6908461183.001130.01`)
- **Buchungszeitraum je Projekt** (Start-/Enddatum, optional): Stunden lassen sich nur
  innerhalb des Zeitraums buchen – Tageszellen außerhalb sind im Forecast gesperrt.
  Daraus abgeleitet werden **Burnrate** (h/Woche bzw. h/Arbeitstag), Resttempo und ein
  Warnhinweis, falls außerhalb des Zeitraums gebucht wurde
- Mehrwochen-Forecast-Ansicht: Projekte × Tage (Mo–Fr) über mehrere Wochen,
  **ein Stundenwert pro Tag** (vergangene Tage = gebucht, ab heute = Forecast),
  automatische Summen (Summenspalte zentriert), Buttons zum Leeren einzelner Tage
  oder ganzer Wochen
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
| `FORECAST_API_READ_TOKEN`  | – | Bearer-Token für **Lesezugriff** (GET) auf die JSON-API (`/api/v1`) |
| `FORECAST_API_WRITE_TOKEN` | – | Bearer-Token für **Lese- und Schreibzugriff** auf die JSON-API |

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
eine kompakte Direktive `forecastPlan` (`projectId`, `fiscalYear`, `hoursPerWeek`).
Der Server expandiert diese automatisch in Mo–Fr-Einträge (`hoursPerWeek/5` pro
Werktag) für das gesamte Fiskaljahr.

## HTTP-API
Unter `/api/v1` steht eine JSON-API bereit, um den Forecast von externen Tools
(z. B. einem Desktop-Client) zu **lesen** und **zu synchronisieren** (ein
Stundenwert je Tag/Projekt), Projekte zu verwalten und Einstellungen zu pflegen. Die
Web-Oberfläche bleibt bewusst ohne Authentifizierung; **nur `/api/**` ist über
zwei Bearer-Tokens geschützt**:

| Token-Env-Variable          | Zugriff                         |
|-----------------------------|---------------------------------|
| `FORECAST_API_READ_TOKEN`   | nur Lesen (GET)                 |
| `FORECAST_API_WRITE_TOKEN`  | Lesen **und** Schreiben         |

Ist **keiner** der beiden Tokens gesetzt, ist die API deaktiviert (`503`). Die
Tokens werden – wie der KI-Key – **nicht** in `data.json` gespeichert, sondern
nur über Umgebungsvariablen bereitgestellt. In den **Einstellungen** wird
angezeigt, ob die beiden Variablen gesetzt sind.

```bash
# Aktuellen Stand lesen
curl -H "Authorization: Bearer $READ" https://host/api/v1/data

# Stunden synchronisieren (Upsert je Tag/Projekt; hours=0 löscht)
curl -X POST https://host/api/v1/entries/sync \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"entries":[{"date":"2026-07-01","projectId":"<id>","hours":6}]}'
```

Die vollständige Referenz aller Endpunkte, Parameter und Beispiele steht in
[docs/API.md](docs/API.md).

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
docker volume create forecast-data
docker run -p 8080:8080 -v forecast-data:/appdata forecast-tool
```

## Mit docker compose (Ziel-Host)
`docker-compose.yml` zieht standardmäßig das fertige Image aus der GitHub
Container Registry und persistiert die Daten in einem Named Volume:
```bash
docker compose up -d
```
Möchtest du lokal bauen statt zu ziehen, kommentiere in `docker-compose.yml`
die `image:`-Zeile aus und aktiviere `build: .`.

## CI/CD: Prüfungen und Container-Release
Der Workflow [.github/workflows/ci.yml](.github/workflows/ci.yml) prüft Format,
`go vet`, golangci-lint, govulncheck, gosec (SARIF), Race-Tests und den
statischen Build. [.github/workflows/release.yml](.github/workflows/release.yml)
baut und pusht Multi-Arch-Images nach GHCR, erzeugt SBOM/Provenance, signiert
keyless mit cosign und lädt den Trivy-SARIF-Report hoch.

Jeder Push auf `main` baut und pusht ein Multi-Arch-Image, das als `latest`
getaggt wird (`ghcr.io/daknoblo/forecast-tool:latest`). Es gibt keine weiteren
Stages (kein `stable`/`dev`) mehr.

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

### Fehlerbehebung: `open /appdata/data.json: permission denied`
Das Image läuft als **Non-Root-User (UID 65532)** und muss nach `/appdata`
schreiben. Der Fehler tritt auf, wenn das gemountete Datenverzeichnis einem
anderen Nutzer gehört (meist `root`, z. B. aus einer älteren, als root
laufenden Image-Version).

**Named Volume** (Standard dieses Repos): Ein frisch angelegtes Volume erhält
automatisch die richtigen Rechte; die mitgelieferte `docker-compose.yml` setzt
sie über einen kleinen `init-permissions`-Container zusätzlich bei jedem Start:
```bash
docker compose up -d
```
Einmaliger manueller Fix für ein bestehendes Named Volume:
```bash
docker compose down
VOL=$(docker volume ls -q | grep forecast-data | head -1)
docker run --rm -v "$VOL":/appdata alpine chown -R 65532:65532 /appdata
docker compose up -d
```

**Bind Mount** (z. B. `./appdata:/appdata`, häufig bei Dockge/Portainer): Hier
initialisiert Docker die Rechte **nicht** automatisch – das Host-Verzeichnis
muss dem Container-User (UID 65532) gehören. Entweder einmalig auf dem Host im
Stack-Verzeichnis:
```bash
sudo chown -R 65532:65532 ./appdata
```
… oder – damit es bei jedem Deploy automatisch passiert – denselben
`init-permissions`-Service (mit **demselben** Bind-Mount) in die Compose
aufnehmen und `forecast` per `depends_on` darauf warten lassen:
```yaml
  init-permissions:
    image: alpine:3
    command: ["chown", "-R", "65532:65532", "/appdata"]
    volumes:
      - ./appdata:/appdata
    restart: "no"
  forecast:
    # ... bisherige Konfiguration ...
    depends_on:
      init-permissions:
        condition: service_completed_successfully
```

## Tests
```bash
go test ./...
```

## Projektstruktur & Plan
Siehe [docs/PLAN.md](docs/PLAN.md) für Architektur und Designentscheidungen.