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
  `sync.RWMutex`). Pfad über `FORECAST_DATA_DIR` überschreibbar (`DATA_DIR` nur Legacy-Alias).
- Feiertage automatisch via `github.com/rickar/cal/v2` (deutsche Bundesländer wählbar);
  Abdeckung year-1..year+2, da das Fiskaljahr zwei Kalenderjahre umspannt.
- Keine Authentifizierung (nur intern). Server lauscht auf `:8080` (`FORECAST_ADDR`).
- Deployment: Dockerfile (distroless, non-root, multi-arch, Healthcheck) + GitHub Actions → `ghcr.io/daknoblo/forecast-tool`.
- Vorgschaltete reverse-proxy mit traefik, port 8080 ist also intern

## Datenmodell

- `Project`: id, assignmentId, name, budgetHours, active, color, startDate, endDate.
  `assignmentId` (String, externe Assignment-ID z. B. `5641245`) identifiziert
  reale Projekte eindeutig und ist beim Anlegen (Web-Formular + `POST /api/v1/projects`)
  **Pflicht** (max. 100 Zeichen); das Urlaubsprojekt hat keine `assignmentId`.
  `startDate`/`endDate` (ISO `YYYY-MM-DD`, inklusiv, beide optional/`omitempty`)
  grenzen den **Buchungszeitraum** ein; leer = ganzes Fiskaljahr. `Project.Bookable(iso)`
  prüft per lexikografischem String-Vergleich, ob ein Datum im Zeitraum liegt.
  `Validate` prüft Datumsformat und `startDate <= endDate`.
- `Entry`: date (YYYY-MM-DD), projectId, hours. Pro Tag und Projekt gibt es genau
  **einen** Stundenwert; ob er als gebucht (Ist) oder Forecast zählt, ergibt sich aus
  dem Datum (vergangene Tage = gebucht, heute und später = Forecast). Das frühere
  `kind`-Feld ist nur noch ein Migrations-Altfeld (wird beim Laden in `storage.normalize`
  über `mergeEntries` kollabiert: pro (date, projectId) ein Wert, Ist gewinnt) und wird
  nicht mehr geschrieben.
- `Settings` (global): year (= aktives Fiskaljahr), federalState, weeklyTargetHours,
  fiscalYearStartMonth, `ai` (AISettings), `utilization` (UtilizationSettings).
- `UtilizationSettings` (global, in `Settings.Utilization`): die Auslastungs-Ampel.
  Drei Schwellen (`minHours` 26, `optimalHours` 40, `overHours` 60) und vier frei
  editierbare Labels (`minLabel` „Burnrate Minimum“, `optimalLabel` „Optimal“,
  `highLabel` „Zu hoch“, `overLabel` „Überbucht“). `Settings.ClassifyUtilization(h)`
  ordnet Wochenstunden einem `UtilStatus{Key,Label,Hours}` zu: `h<=min`→`min`
  (blau, Pfeil runter) · `min<h<=optimal`→`optimal` (grün, OK) ·
  `optimal<h<over`→`high` (orange, Pfeil hoch) · `h>=over`→`over` (rot, ✕).
  Basis ist die Wochenbuchung (Summe der Stundenwerte, ohne Urlaub).
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
- **Auslastungs-Ampel** über `models.DefaultUtilization()` (greift in `DefaultData`
  und in `storage.normalize`, wenn alle drei Schwellen `0` sind → Altdaten):
  **min 26 / optimal 40 / over 60 h** mit Default-Labels (s. o.).
- Pro Fiskaljahr über `models.DefaultFYSettings()` (greift in `FYFor`, wenn ein FY
  noch nicht konfiguriert ist): **Ziel 1440 h**, **Urlaub H1 15 / H2 15 Tage**,
  **Standard Tasks 250 h**. Nicht konfigurierte FY werden damit in den Einstellungen
  vorbefüllt.

## Fiskaljahr (FY)

- Das FY ist nach dem Kalenderjahr benannt, in dem es **endet**. Es beginnt am 1. des
  `FiscalYearStartMonth` im **Vorjahr** von `Year` (z. B. `Year=2027`, Start Juli →
  **01.07.2026–30.06.2027**). Startmonat Januar = Kalenderjahr (keine Verschiebung).
  Einzige Quelle der Wahrheit ist `forecast.FiscalYear(year, startMonth)`.
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

## Urlaub als Projekt (auto-verwaltet)

- Pro Fiskaljahr existiert genau **ein automatisch verwaltetes Urlaubs­projekt**
  (`Project.System == "vacation"`, `models.VacationSystem`), stabile ID
  `vacation-<Jahr>` (`models.VacationProjectID`), Name „Urlaub“, feste Farbe
  `models.VacationColor` (#64748b). Es wird über `models.EnsureVacationProject(d, year)`
  angelegt/synchronisiert – aufgerufen in `storage.normalize` (Laden + JSON-Editor +
  Erststart via `load()`), beim FY-Wechsel (`handleSetActiveFY`) und beim Speichern der
  Einstellungen (`handleSettingsSave`).
- **Budget = (VacationDaysH1 + VacationDaysH2) × 8 h** aus den FY-Einstellungen
  (`FiscalYearSettings.VacationBudgetHours`). Es ist **gesperrt** (kein Bearbeiten/
  Umbenennen über `handleProjectUpdate`) und **nicht löschbar** (`handleProjectDelete`-Guard;
  auch im JSON-Editor wird es durch `normalize` wiederhergestellt).
- Urlaubsstunden sind **rein informativ**: Sie zählen **nicht aufs FY-Ziel**
  (`BuildGoalSummary` überspringt Urlaub) und **nicht in die Wochen-Auslastungs-Ampel**
  (`BuildWeek`/`BuildSpan` `EffectiveTotal`, `BuildYearSummary` `WeekTotals`). Urlaub
  bleibt aber ein **normales Projekt** (buchbar im Forecast-Grid,
  eigenes Budget/Burndown, eigene Zeilen-/Wochensummen). Projekte-Seite zeigt Badge
  „automatisch · Urlaub“ statt der Bearbeiten/Löschen-Steuerung.
- AI-Blueprint (`internal/ai`) enthält das Urlaubsprojekt inkl. `system`-Feld; die KI darf
  es nicht löschen, umbenennen oder sein Budget ändern.

## Standard Tasks

- Stundenanzahl für Standard Tasks für das gesamte FY (ein Eingabefeld in den
  Einstellungen). Das Label (`standardTaskLabel`) bleibt im Datenmodell erhalten,
  wird aber nicht mehr über die Einstellungen gepflegt.
- Werden **wie Feiertage und Urlaub von den FY-Gesamtstunden abgezogen**.

## Zielrechnung & Kapazität

- Feiertage (Mo–Fr) zählen **nicht** aufs FY-Ziel; nur die erfassten Stunden (vergangene
  Tage = gebucht, ab heute = Forecast).
  Feiertage sind rein informativ (8 h/Tag, eigene Kategorie).
- FY-Gesamtstunden = alle FY-Wochentage × 8 h (ohne Wochenende).
- **Verfügbare Arbeitsstunden (netto) = Wochentagsstunden − Feiertage − Urlaub (H1+H2) − Standard Tasks.**
- Kennzahlen: % des Ziels von FY-Gesamtstunden und von verfügbaren Stunden.
- Resttempo bis FY-Ende: Restziel (Ziel − Ist), verbleibende Arbeitstage, benötigte h/Tag.
- Soll pro Woche/Monat/Quartal = Ziel arithmetisch gleich aufteilen.

## Projekt-Buchungszeitraum & Burnrate

- Jedes Projekt hat einen Buchungszeitraum (`startDate`/`endDate`, inklusiv; leer = FY).
  `forecast` clampt den Zeitraum aufs FY (`projectWindow(p, fyStart, fyEnd)`).
- `ProjectSummary` (aus `BuildYearSummary(d, cal)`) trägt zusätzlich:
  `StartDate`/`EndDate` + `StartLabel`/`EndLabel` (DD.MM.YYYY), `HasCustomWindow`,
  `WindowWorkdays` (Mo–Fr ohne Feiertage im Zeitraum, `cal`-basiert), `BurnPerWeek`
  (= Budget / (Arbeitstage/5)), `BurnPerWorkday` (= Budget / Arbeitstage),
  `RemainingWorkdays` (ab heute bis Zeitraum-Ende), `RequiredPerWorkday`
  (= Restbudget / Rest-Arbeitstage) und `OutOfWindow` (effektive Stunden, die
  außerhalb des Zeitraums gebucht wurden – Warnhinweis).
- **`BuildYearSummary` nimmt jetzt `cal *holidays.Calendar`** (für feiertagsgenaue
  Arbeitstage). Aufrufer: `handleDashboard`/`handleProjects`/`handleGoal` (alle haben
  `s.calendar(d)`), Tests übergeben `holidays.New(2026, "BY")`.
- **Buchungssperre außerhalb des Zeitraums:** Im Forecast-Grid (`week.html`) werden
  Tageszellen außerhalb des Zeitraums über die Template-Funktion `bookable $p $d.Date`
  als `td.day.closed` (mit `–`, ohne Inputs) gerendert. `handleWeekSave` erzwingt dies
  zusätzlich serverseitig (`p.Bookable(date)`-Guard beim Re-Add), sodass auch manuelle
  POSTs außerhalb des Zeitraums verworfen werden.
- Projekte-Seite zeigt Zeitraum, Arbeitstage, Burnrate (h/Woche · h/Tag), Resttempo
  und ggf. die „außerhalb des Zeitraums“-Warnung; Dashboard hat Spalten „Zeitraum“
  und „Burnrate“.

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
- **Dashboard-Auslastungs-Sankey:** Die Dashboard-Seite ist `Wide` (volle Breite) und
  zeigt – nach den KPI-Karten, vor „Budgets“ – eine Karte „Auslastung“ mit einem
  serverseitig gerenderten, JS-freien Sankey/Alluvial-Diagramm (`web.sankeySVG` aus
  `forecast.BuildSankey`). Darüber Zeitraum-Umschalter (`forecast.SankeyRanges`:
  1 Woche/2 Wochen/4 Wochen/2 Monate/3 Monate/Halbjahr/Fiskaljahr) als `.chip`-Links
  (`GET /?sankey=<key>`, Default `4w`, unbekannt → Default via `NormalizeSankeyRange`).
  Buckets sind Wochen (bis 2 Monate) bzw. Monate (ab 3 Monaten, Halbjahr, Fiskaljahr);
  nur Tage **innerhalb des FY** zählen. Projekte sind farbige, gestapelte Bänder (Höhe ∝
  geplante Stunden, Ribbons zwischen benachbarten Buckets, Stapelreihenfolge nach
  Gesamtstunden); das **Urlaubsprojekt ist ausgeschlossen** (Auslastungs-Konvention).
  Vertikale Trenner grenzen die Wochen/Monate ab, jede Spalte ist mit den **summierten
  geplanten Projektstunden** beschriftet; Summe und eine Legende (Stunden je Projekt)
  stehen darunter.
- Auf der Ziel-Seite werden **Quartals- und Monatsübersicht immer angezeigt**
  (nicht ausklappbar).
- **Ziel-Seite Reihenfolge (chronologisch):** Gesamt-FY (KPIs, Status inkl.
  Fortschritts-Diagramm, FY-Kapazität, Resttempo, Soll-Tempo) → **Halbjahre H1 & H2**
  (`GoalSummary.Halves`, je Card mit Kennzahlen, Auslastungsbalken und kleinem
  Fortschritts-/Burn-Diagramm) → Quartale → Monatsübersicht → Wochenauslastung.
  Die Diagramme (`web.progressSVG`, kumulierte Hochrechnung vs. Ideallinie + Ziel)
  gibt es für FY, H1 und H2; Monate/Wochen behalten die Balken.
- **Forecast-Grid Layout:** Projektnamen-Spalte (`.pname`) breit (~240 px), alle Werte
  zentriert (außer `.pname`), Projektzeilen durch einen horizontalen Rahmen getrennt
  (`tbody td` border-bottom 2px), Wochensummen-Spalte (`.weeksum`) schmal an den Inhalt
  angepasst. Diese Vorgaben liegen in `static/style.css` (kein Markup in `week.html` nötig).
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
- **Auslastungs-Ampel-Karte (Einstellungen):** eigene Karte „Auslastungs-Ampel (global)“
  mit eigenem Formular (`section=utilization`): drei Schwellen (`utilMin`/`utilOptimal`/
  `utilOver`) und vier Label-Felder (`utilMinLabel`/`utilOptimalLabel`/`utilHighLabel`/
  `utilOverLabel`). Steht zwischen der Pro-FY-Karte und der KI-Endpoint-Karte.
- **Forecast-Seite (`/week`):** Das Grid ist **pro Woche gruppiert** (`.Span.Blocks`):
  nach den fünf Tagesspalten folgt je Woche eine **Wochensummen-Spalte** (`.weeksum`,
  Stundensumme pro Projekt), ganz rechts eine **Gesamt-Spalte** über alle sichtbaren Wochen
  (`.grandsum`). Wochen sind durch einen dickeren linken Rahmen am ersten Tag (`.weekstart`)
  abgegrenzt; **Monatsenden** durch einen farbigen rechten Strich (`.monthend`, gesetzt über
  `DayCell.MonthEnd`: nächster sichtbarer Wochentag liegt in einem neuen Monat).
  Vergangene (gebuchte) Tage sind dezent markiert (`td.day.past`, Badge „gebucht“). Die
  `tfoot`-Zeile „Stunden / Tag“ (`.dayfoot`) ist **zentriert** (`td.center`) und hat
  ebenfalls Wochen-/Gesamtsummen. Header/Tages-/Footer-Zeilen
  iterieren alle über `.Span.Blocks` → `.Days`, damit Spalten bündig bleiben; die
  Wochensummen-Header sind `rowspan=2`.
- **Burnrate-Banner (`/week`):** über der Tabelle, direkt **unter** der zentrierten
  Steuerzeile, zeigt `.burnbanner`
  die kombinierte Burnrate (`{{.Burn.PerWeek}}` h/Woche · `{{.Burn.PerWorkday}}` h/Tag) plus
  Pro-Projekt-Chips für alle **aktiven** Projekte, deren Buchungsfenster den sichtbaren
  Zeitraum überlappt. Quelle: `forecast.BuildSpanBurn(ys.Projects, spanStart, spanEnd)`
  in `handleWeek` (`ys` = `BuildYearSummary(d, cal)`).
- **Forecast-Steuerung & Auto-Speichern (`/week`):** Über der Tabelle steht eine **zentrierte**
  Steuerzeile (`.week-controls`): der „Sichtbare Wochen“-Umschalter wird links von einem
  **«zurück»**- und rechts von einem **weiter»**-Button (`.btn.nav-btn`, an den FY-Rändern
  deaktiviert) flankiert; darunter das Burnrate-Banner. Es gibt **keinen Speichern-Button** –
  Änderungen werden **automatisch** gespeichert: Tippen (debounced) bzw. Verlassen/Enter einer
  Zelle schickt sie per `fetch` (JSON, `keepalive`) an **`POST /week/cells`**
  (`{cells:[{date,projectId,hours}]}`; `hours<=0` löscht; Projekt-Existenz + `p.Bookable`-Guard,
  Verworfene werden gezählt; Persistenz über `store.Mutate`). Die Seite wird beim Eintragen
  **nie neu geladen**; eine Status-Pille (`[data-save-status]`: „Automatisch gespeichert“ /
  „Speichert…“ / „Gespeichert ✓“ / „Fehler beim Speichern“) gibt Rückmeldung. Grid-Zeilen und
  Eingabefelder sind ~20 % größer; die **Urlaubszeile** ist dezent eingefärbt (`tr.vacrow`), und
  eine Leerzeile (`tr.footspacer`) koppelt die Summen-/Auslastungszeilen im `tfoot` optisch vom
  Rest ab. Der frühere Bulk-`POST /week/{week}` bleibt als Fallback erhalten.
- **Leeren-Buttons** (`.clearbtn`, `type=button`, `data-clear-dates`) in Wochengruppen- und
  Tages-Kopfzeilen leeren per JS alle `input.hcell` mit passendem `_<datum>`-Suffix; die
  geleerten Zellen werden ebenfalls automatisch gespeichert (`hours 0` → löschen). Eine
  **Status-Zeile** im `tfoot` zeigt je Woche (`colspan=6`: 5 Tage + Summenspalte) den
  Ampel-Punkt plus die (urlaubsbereinigten) Wochenstunden.
- **Projekte-Seite:** KPI-Zeile zeigt Budget, Verbraucht, Rest, **Burnrate** (h/Woche) und
  Auslastung; darunter der Zeitraum-/Burnrate-Block (`.project-window`).
- **Ampel-Punkte** werden über das Template-Partial `{{define "utilstatus"}}` (in
  `partials.html`) gerendert: farbiger Kreis (`.util-dot`) mit weißem Symbol (↓ / OK /
  ↑ / ✕) + Label. Erscheinen in der Forecast-Status-Zeile sowie in der Spalte „Status“
  der Wochentabellen von Dashboard (`.Summary.WeekTotals`) und Zielen (`.WeekTotals`,
  von `handleGoal` per FY-gefiltertem `BuildYearSummary` übergeben).

## Arbeitskonventionen (für den Agenten)

- Vor dem Commit: `gofmt`, `go vet ./...`, `go build ./...`, `go test ./...` müssen grün sein.
- Danach lokaler Smoke-Test (Server mit temporärem `FORECAST_DATA_DIR`), anschließend aufräumen
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

## HTTP-API (`/api/v1`)

- Eigenes Paket `internal/api` (nur stdlib), gemountet in `web.Handler()` per
  `mux.Handle("/api/", api.New(store, logger))`. Eigener `ServeMux` mit **vollen**
  Pfaden `GET /api/v1/...` (kein StripPrefix), umschlossen von der Auth-Middleware.
- **Zwei Bearer-Tokens** über Umgebungsvariablen (exportierte Konstanten
  `api.ReadTokenEnv` = `FORECAST_API_READ_TOKEN`, `api.WriteTokenEnv` =
  `FORECAST_API_WRITE_TOKEN`). Read = nur `GET`; Write = Read + Schreiben. Vergleich
  konstant-zeitlich (`crypto/subtle`). Kein/ungültiger Token → `401`, Lese-Token auf
  Schreib-Endpunkt → `403`, **beide** Tokens leer → `503` (fail-closed). Tokens werden
  **nie** in `data.json` gespeichert und **nie** geloggt. **Die HTML-UI bleibt ohne Auth.**
- **Einstellungen** zeigen (read-only Karte „API-Zugriff", zwischen KI-Endpoint- und
  Konfigurationsdatei-Karte) an, ob die beiden Token-Variablen gesetzt sind – analog zur
  KI-Key-Statusanzeige (`APIReadSet`/`APIWriteSet` in `handleSettings`).
- **Schreibpfade laufen über `store.Mutate`** (Copy-on-Write: klont → mutiert →
  `normalize` → `models.Validate` → nur bei Erfolg persistieren+swappen). Reads über
  `Snapshot()`/`Marshal()`. `GET`-Antworten **redigieren** den KI-Key (`AI.APIKey=""`).
- **Endpunkte:** Read (`GET`): `/data`, `/settings`, `/projects[?fiscalYear=&all=]`,
  `/projects/summary[?fiscalYear=]` (berechnete Verbraucht/Rest/Auslastung je Projekt aus
  `BuildYearSummary`→`ProjectSummary`), `/projects/{id}`, `/entries[?from=&to=&projectId=]`,
  `/goal[?year=]`. Write:
  `POST /entries/sync`, `POST /projects`, `PUT /projects/{id}`, `DELETE /projects/{id}`,
  `PUT /settings`, `PUT /settings/fiscal-years/{year}`.
- **`POST /entries/sync`** ist der Kern: Upsert je `(date, projectId)`, `hours=0`
  löscht; Guard über Projekt-Existenz + `p.Bookable(date)`; verworfene Einträge werden in
  `skipped` gemeldet (Rest wird angewendet). Antwort `{upserted, deleted, skipped}`.
- Urlaubsprojekt bleibt gesperrt: `PUT`/`DELETE` darauf → `409`. FY-Settings-`PUT`
  synchronisiert das Urlaubsbudget via `EnsureVacationProject`.
- Fehlerformat `{ "error": "<deutsch>" }`. Request-Body strikt (`DisallowUnknownFields`),
  Limit via `http.MaxBytesReader` (2 MiB). Referenz-Doku: `docs/API.md` (bei
  Schema-/Endpunkt-Änderungen mitpflegen), Env-Variablen in README/`.env.example`/compose.

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