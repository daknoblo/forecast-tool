# forecast-tool

Ein leichtgewichtiges Single-User **Forecast-Tool** in Go mit Web-Oberfläche.
Lege Projekte mit Stundenbudget an, forecaste tageweise (Mo–Fr) Stunden pro
Projekt und behalte Wochen-/Jahressummen, Auslastung und Restbudget im Blick.
Feiertage werden je nach Bundesland automatisch berücksichtigt.

Daten werden für **ein Kalenderjahr** in einer einfachen JSON-Datei unter
`appdata/data.json` gespeichert – keine Datenbank nötig.

## Features
- Projekte mit Stundenbudget (CRUD, Farbe, aktiv/inaktiv)
- Wochenansicht: Projekte × Mo–Fr, Eingabe pro Tag, automatische Wochensummen
- Automatische Feiertage (alle 16 Bundesländer wählbar)
- Konfigurierbare Wochensollstunden, Auslastung in %
- Restbudget je Projekt + Burn-Down-Diagramm (server-seitiges SVG)
- Dashboard mit Jahresübersicht und Wochenauslastung
- Daten als JSON im Volume, läuft als schlanker Container

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