---
description: "Fachliche Anforderungen und Konventionen für das Forecast-Tool (Go-Webapp, Fiskaljahr, Kapazitäts-/Zielrechnung). Verwenden bei jeder Änderung an Datenmodell, Aggregation, Handlern oder Templates."
applyTo: "internal/**,cmd/**"
---

# Forecast-Tool – Anforderungen & Konventionen

Persönliches Single-User-Forecast-Tool (Go-Webapp → Docker → GHCR). Diese Datei
sammelt alle bisher formulierten Anforderungen als verbindliche Referenz.

## Grundlagen / Stack

- **Sprache der UI: Deutsch.** Antworten an den Nutzer ebenfalls auf Deutsch.
- Go (Standardbibliothek): `net/http` ServeMux (Methode+Pattern-Routing, `r.PathValue`),
  `html/template` + `embed`. Modulpfad `github.com/daknoblo/forecast-tool`.
- Speicher: **eine JSON-Datei** unter `appdata/data.json` (atomar via temp + rename,
  `sync.RWMutex`). Pfad über `DATA_DIR` überschreibbar.
- Feiertage automatisch via `github.com/rickar/cal/v2` (deutsche Bundesländer wählbar);
  Abdeckung year-1..year+2, da das Fiskaljahr zwei Kalenderjahre umspannt.
- Keine Authentifizierung (nur intern). Server lauscht auf `:8080` (`PORT`-env).
- Deployment: Dockerfile (alpine, multi-arch) + GitHub Actions → `ghcr.io/daknoblo/forecast-tool`.
- Vorgschaltete reverse-proxy mit traefik, port 8080 ist also intern

## Datenmodell

- `Project`: id, name, budgetHours, active, color.
- `Entry`: date (YYYY-MM-DD), projectId, hours, kind (`forecast` | `actual`).
  Ist-Stunden überschreiben Forecast pro Tag+Projekt bei der Budget-/Verbrauchsrechnung.
- `Settings` (global): year (= aktives Fiskaljahr), federalState, weeklyTargetHours,
  fiscalYearStartMonth, `ai` (AISettings).
- `AISettings` (in `Settings.AI`): endpoint, deployment, apiVersion. Konfiguriert
  einen entfernten, Azure-OpenAI-kompatiblen Chat-Completions-Endpoint (z. B. Azure AI
  Foundry Model-Router). Der **API-Key wird NICHT in der JSON gespeichert**, sondern
  ausschließlich über die Umgebungsvariable `FORECAST_AI_API_KEY` bereitgestellt
  (`AISettings.APIKey` ist `omitempty` und nur noch Legacy-Fallback beim Lesen).
- `FiscalYearSettings` (pro FY, in `Data.FiscalYears map[int]...`): targetHours,
  vacationDaysH1, vacationDaysH2, standardTaskLabel, standardTaskHours.
- Legacy-Felder (`fiscalYearTargetHours`, `annualVacationDays`) nur noch für Migration
  + Fallback (`FYFor`) behalten, `omitempty`.

## Standardwerte (Defaults)

- **Bundesland: `SN` (Sachsen)** – in `models.DefaultData` und `storage.normalize`.
- **Wochensollstunden: `40`** (auch als grauer `placeholder="40"` im Eingabefeld).
- Pro Fiskaljahr über `models.DefaultFYSettings()` (greift in `FYFor`, wenn ein FY
  noch nicht konfiguriert ist): **Ziel 1440 h**, **Urlaub H1 15 / H2 15 Tage**,
  **Standard Tasks 250 h**. Nicht konfigurierte FY werden damit in den Einstellungen
  vorbefüllt.

## Fiskaljahr (FY)

- FY beginnt am 1. des `FiscalYearStartMonth` im Kalenderjahr `Year`
  (z. B. Start Juli → 01.07.2026–30.06.2027). Startmonat Januar = Kalenderjahr.
- **H1 = erste 6 FY-Monate**, **H2 = letzte 6 FY-Monate**.
- Wochen-/Quartalsansichten sind FY-relativ (Wochen-Index über die Jahresgrenze).
- Quartale in Reihenfolge ab FY-Start (z. B. Jul–Sep, Okt–Dez, Jan–Mär, Apr–Jun).
- projekte gehören immer in ein Fiskaljahr (nicht übergreifend), sofern ein projekt über ein FY geht wird dies neu angelegt. deswegen müssen die projekte auch für jedes FY neu angelegt und gespeichert werden
- Einträge (Forecast/Ist) gehören immer zu einem Datum, das wiederum in ein FY fällt

## Einstellungen pro Fiskaljahr

- Ziel, Urlaub und Standard Tasks werden **pro Fiskaljahr** gespeichert
  (Werte ändern sich je FY). Globale Werte (Startmonat, Bundesland, Wochensoll)
  gelten für alle FY.
- Einstellungsseite erlaubt Wechsel des betrachteten FY (`?year=`); beim Speichern
  wird das betrachtete FY zum aktiven FY.

## Urlaub (pro Halbjahr)

- Urlaubsanspruch zählt **pro Kalenderjahr**, das Fiskaljahr erstreckt sich über
  zwei Kalenderjahre. Daher Eingabe **getrennt für H1 und H2** (Tage à 8 h).
- In der Kapazität getrennt als „Urlaub 1. Halbjahr“ / „Urlaub 2. Halbjahr“ ausweisen.

## Standard Tasks

- Stundenanzahl für Standard Tasks für das gesamte FY (ein Eingabefeld in den
  Einstellungen). Das Label (`standardTaskLabel`) bleibt im Datenmodell erhalten,
  wird aber nicht mehr über die Einstellungen gepflegt.
- Werden **wie Feiertage und Urlaub von den FY-Gesamtstunden abgezogen**.

## Zielrechnung & Kapazität

- Feiertage (Mo–Fr) zählen **nicht** aufs FY-Ziel; nur reale (Ist) + Forecast-Stunden.
  Feiertage sind rein informativ (8 h/Tag, eigene Kategorie).
- FY-Gesamtstunden = alle FY-Wochentage × 8 h (ohne Wochenende).
- **Verfügbare Arbeitsstunden (netto) = Wochentagsstunden − Feiertage − Urlaub (H1+H2) − Standard Tasks.**
- Kennzahlen: % des Ziels von FY-Gesamtstunden und von verfügbaren Stunden.
- Resttempo bis FY-Ende: Restziel (Ziel − Ist), verbleibende Arbeitstage, benötigte h/Tag.
- Soll pro Woche/Monat/Quartal = Ziel arithmetisch gleich aufteilen.

## UI-Vorgaben

- **Zentraler App-Name:** Konstante `web.AppName` ("Forecast Tool") wird über die
  Template-Funktion `{{appName}}` ausgegeben – in `<title>`, Header-Brand und Footer.
  Name nur an dieser einen Stelle ändern.
- **Navigation (Header)** in dieser Reihenfolge und Beschriftung:
  Dashboard (`/`) – Projekte (`/projects`) – Forecast (`/week`) – Ziele (`/goal`) –
  JSON (`/data`) – Einstellungen (`/settings`). Die Active-Klassen-Schlüssel bleiben
  technisch `dashboard`/`projects`/`week`/`goal`/`data`/`settings` (nur Anzeige + Reihenfolge).
- **Footer:** `{{appName}} · Fiskaljahr {{Year}}` links, rechts ein Link auf das
  GitHub-Profil `https://github.com/daknoblo/` mit Inline-SVG-Icon (kein externes Asset,
  da `embed`). Kein Wochensoll mehr im Footer.
- Auf der Ziel-Seite werden **Quartals- und Monatsübersicht immer angezeigt**
  (nicht ausklappbar).
- **Zentrales FY-Dropdown oben rechts im Header** (dort, wo Jahr/Bundesland stehen):
  schaltet das aktive Fiskaljahr global um, funktioniert von **jeder** Seite und kehrt
  nach dem Wechsel zur Ursprungsseite zurück (Route `POST /fy`, Redirect auf Referer). Sofern man hier das FY wechselt müssen auch auf allen seiten die passenden FY angezeigt werden
- In den Einstellungen **Pfad und Größe der Konfigurationsdatei** (JSON) anzeigen
  (Größe als B/KB/MB). Diese Karte steht **ganz unten** auf der Einstellungsseite
  (nach der KI-Endpoint-Karte).
- **KI-Endpoint-Karte (Einstellungen):** Reihenfolge der Elemente von oben nach unten:
  Eingabefelder (Endpoint/Deployment/API-Version) → `API-Key`-Label mit Statusanzeige
  (env gesetzt / nicht gesetzt) → Hinweis zur Umgebungsvariable → **Speichern-Button
  ganz unten**.

## Arbeitskonventionen (für den Agenten)

- Vor dem Commit: `gofmt`, `go vet ./...`, `go build ./...`, `go test ./...` müssen grün sein.
- Danach lokaler Smoke-Test (Server mit temporärem `DATA_DIR`), anschließend aufräumen
  (`appdata` entfernen, `appdata/.gitkeep` wiederherstellen).
- Keine separaten Markdown-Doku-Dateien anlegen, außer ausdrücklich gewünscht.
- Templates/Static liegen unter `internal/web/` (per `embed`), nicht im Repo-Root.

## Export

- es soll die möglichkeit die aktuelle JSON aus der Anwendung heraus zu exportieren bzw. herunterzuladen
- Download-Route `GET /export` (Content-Disposition attachment, Dateiname mit Datum).
  Der Export-Button liegt im JSON-Editor (`/data`), nicht mehr in den Einstellungen.

## JSON-Editor (`/data`)

- Eigene Seite „JSON" in der Navigation: großes, breites Textfeld (volle Kartenbreite)
  zum direkten Bearbeiten der **gesamten** Datendatei im Browser – u. a. um KI-generiertes
  JSON einzufügen.
- **Validierung vor dem Speichern** (`store.ReplaceJSON` → `models.Validate`): striktes
  Parsen (`DisallowUnknownFields`, keine Trailing-Daten), referentielle Prüfungen
  (z. B. jede `entries.projectId` muss existieren). Ungültige Eingaben werden mit
  deutscher Fehlermeldung abgelehnt, **die Eingabe bleibt erhalten**, der Store wird
  nie überschrieben. Erfolgreiches Speichern zeigt die kanonische (normalisierte) Form.
- Persistenz weiterhin atomar (temp + rename).

## KI-Aktualisierung der JSON

- KI-Endpoint wird in den **Einstellungen** konfiguriert (eigenes Formular, `section=ai`):
  Endpoint-URL, Deployment/Model-Router-Name, API-Version. Der **API-Key** kommt aus der
  Umgebungsvariable `FORECAST_AI_API_KEY` (Docker-Secret/`environment`), nicht aus der UI.
  Beim Speichern wird ein evtl. vorhandener Legacy-Key aus der Datendatei entfernt.
  Effektive Settings via `effectiveAI()` (env überlagert Store).
- Im JSON-Editor gibt es ein **Prompt-Feld**; `POST /data/ai` schickt Prompt **und den
  aktuellen (ggf. bearbeiteten) Editor-Inhalt** an den Endpoint und schreibt das Ergebnis
  zurück in das Textfeld. Ohne konfigurierten Endpoint erscheint stattdessen ein Hinweis.
- KI-Client liegt in `internal/ai` (nur stdlib): Azure-OpenAI-kompatible URL
  `{endpoint}/openai/deployments/{deployment}/chat/completions?api-version=...`, Auth via
  `api-key`-Header, `response_format: json_object`, `temperature: 0`, Timeout, entfernt
  Markdown-Fences. Deutsche Fehlermeldungen.
- Dem System-Prompt wird ein **Blueprint** (`ai.Blueprint`) mitgesendet – ein vollständiges,
  gültiges Beispiel-Dokument –, damit das entfernte Modell Feldnamen, Verschachtelung und
  Werttypen des Forecast-JSON kennt. Bei Schema-Änderungen am Datenmodell den Blueprint
  mitpflegen.
- **Kompakte Forecast-Direktiven (`forecastPlan`):** Statt hunderte Tageseinträge
  auszuschreiben (was das Token-Limit sprengt und zu abgeschnittenen Antworten führt),
  gibt die KI für regelmäßige, über ein ganzes FY gleichmäßig verteilte Forecasts genau
  **einen** Eintrag pro Projekt in `forecastPlan` aus: `{ projectId, fiscalYear,
  hoursPerWeek, kind }`. `ai.ExpandPlan` expandiert das **serverseitig** deterministisch
  in Mo–Fr-Einträge (`hoursPerWeek/5` pro Werktag) für das gesamte FY, dedupliziert gegen
  vorhandene Einträge und entfernt `forecastPlan` (Cap `maxExpandedEntries`). Die Expansion
  läuft in `handleDataAI` **vor** der Validierung; `entries` bleibt nur für einzeln genannte
  Tage. `forecastPlan` ist kein Bestandteil von `data.json` (nur Transportformat).
- **Die KI-Antwort wird nie automatisch gespeichert**: Sie wird nur eingefügt und sofort
  via `store.ValidateJSON` geprüft. Speichern erfolgt erst beim expliziten „Speichern"
  (durchläuft erneut die volle Validierung).
- Abgeschnittene KI-Antworten (`finish_reason: length`) werden erkannt und mit
  deutscher Meldung gemeldet; der Client setzt `max_completion_tokens` (32768).

## Logging

- Logging-Paket `internal/logging` (nur stdlib): `Setup(dataDir)` liefert einen
  `*slog.Logger`, der **gleichzeitig auf stdout (Docker-Container-Output) und in eine
  Datei** `appdata/forecast.log` schreibt (Text-Handler).
- **Selbst-Rotation**: bei Überschreiten von **10 MB** (`DefaultMaxBytes`) wird die Datei
  rotiert (`forecast.log.1..3`, `DefaultMaxBackups`), älteste wird verworfen. Kein externes
  Paket.
- `main.go` ruft `logging.Setup` auf, setzt `slog.SetDefault` und leitet die Standard-
  `log`-Ausgabe über einen Adapter in denselben Logger (alles landet in Container-Output
  **und** Logdatei).
- **KI-Nutzung wird protokolliert** (Endpoint/Deployment/API-Version, Prompt-/JSON-Größe,
  Status, `finish_reason`, Token-Usage, Dauer, Erfolg/Fehler/Truncation) – **niemals der
  API-Key**. Fehler/Warnungen erscheinen so auch im Container-Output zum Debuggen.
- `forecast.log*` sind git-ignored.