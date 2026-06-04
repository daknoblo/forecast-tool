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
  fiscalYearStartMonth.
- `FiscalYearSettings` (pro FY, in `Data.FiscalYears map[int]...`): targetHours,
  vacationDaysH1, vacationDaysH2, standardTaskLabel, standardTaskHours.
- Legacy-Felder (`fiscalYearTargetHours`, `annualVacationDays`) nur noch für Migration
  + Fallback (`FYFor`) behalten, `omitempty`.

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

- Freies Feld mit der Bezeichnung "Standard Tasks" in das man eine stundenanzahl für das gesamte FY angeben kann
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

- Auf der Ziel-Seite werden **Quartals- und Monatsübersicht immer angezeigt**
  (nicht ausklappbar).
- **Zentrales FY-Dropdown oben rechts im Header** (dort, wo Jahr/Bundesland stehen):
  schaltet das aktive Fiskaljahr global um, funktioniert von **jeder** Seite und kehrt
  nach dem Wechsel zur Ursprungsseite zurück (Route `POST /fy`, Redirect auf Referer). Sofern man hier das FY wechselt müssen auch auf allen seiten die passenden FY angezeigt werden
- In den Einstellungen **Pfad und Größe der Konfigurationsdatei** (JSON) anzeigen
  (Größe als B/KB/MB).

## Arbeitskonventionen (für den Agenten)

- Vor dem Commit: `gofmt`, `go vet ./...`, `go build ./...`, `go test ./...` müssen grün sein.
- Danach lokaler Smoke-Test (Server mit temporärem `DATA_DIR`), anschließend aufräumen
  (`appdata` entfernen, `appdata/.gitkeep` wiederherstellen).
- Keine separaten Markdown-Doku-Dateien anlegen, außer ausdrücklich gewünscht.
- Templates/Static liegen unter `internal/web/` (per `embed`), nicht im Repo-Root.
