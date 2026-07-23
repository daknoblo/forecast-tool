# Forecast-Tool – HTTP-API

Die JSON-API unter `/api/v1` erlaubt externen Tools (z. B. einem Desktop-Client)
den Forecast zu **lesen** und zu **synchronisieren** (Stunden je Tag/Projekt),
Projekte zu verwalten und Einstellungen zu pflegen.

Die HTML-Oberfläche bleibt bewusst ohne Authentifizierung (nur intern hinter dem
Reverse-Proxy). **Nur `/api/**` ist geschützt** – über zwei Bearer-Tokens.

---

## Authentifizierung

Jeder API-Request muss einen Bearer-Token im `Authorization`-Header senden:

```
Authorization: Bearer <token>
```

Es gibt zwei Tokens, die **ausschließlich** über Umgebungsvariablen bereitgestellt
werden (nicht in `data.json`, werden nie geloggt):

| Umgebungsvariable            | Scope                           | Erlaubte Methoden        |
|------------------------------|---------------------------------|--------------------------|
| `FORECAST_API_READ_TOKEN`    | Lesen                           | `GET`                    |
| `FORECAST_API_WRITE_TOKEN`   | Lesen **und** Schreiben         | `GET`, `POST`, `PUT`, `DELETE` |

Der Schreib-Token schließt Lesezugriff mit ein. In den **Einstellungen** der
Web-Oberfläche wird angezeigt, ob die Variablen gesetzt sind.

### Verhalten

- Kein oder ungültiger Token → `401 Unauthorized` (`WWW-Authenticate: Bearer`).
- Gültiger Lese-Token auf einen Schreib-Endpunkt → `403 Forbidden`.
- **Beide** Tokens nicht gesetzt → die API ist deaktiviert, jeder Request → `503`.

> Tokens werden konstant-zeitlich verglichen. Verwende lange, zufällige Werte,
> z. B. `openssl rand -hex 32`.

---

## Konventionen

- **Basis-URL:** `<host>/api/v1`
- **Content-Type:** Request- und Response-Body sind JSON (`application/json`).
- **Datumsformat:** ISO `YYYY-MM-DD`.
- **Stunden (`hours`):** Dezimalzahl ≥ 0. Pro Tag und Projekt gibt es genau **einen**
  Wert; ob er als **gebucht** (Ist) oder **Forecast** zählt, ergibt sich aus dem Datum
  (vergangene Tage = gebucht, heute und später = Forecast). Es gibt **kein** `kind`-Feld.
- **Fehlerformat:** `{ "error": "<deutsche Meldung>" }` mit passendem HTTP-Status.
- **Request-Body-Limit:** 2 MiB. Unbekannte JSON-Felder werden abgelehnt (`400`).
- **Teil-Updates (`PUT`):** Nur mitgeschickte Felder werden geändert. Bei Projekt-
  Datumsfeldern bedeutet `""` „Zeitraum leeren“, Weglassen „unverändert lassen“.

### Statuscodes

| Code | Bedeutung |
|------|-----------|
| `200` | OK |
| `201` | Ressource angelegt |
| `400` | Ungültige Eingabe (Body, Parameter, Validierung) |
| `401` | Token fehlt oder ist ungültig |
| `403` | Lese-Token auf Schreib-Endpunkt |
| `404` | Ressource nicht gefunden |
| `409` | Konflikt (z. B. automatisch verwaltetes Urlaubsprojekt) |
| `503` | API deaktiviert (keine Tokens konfiguriert) |

---

## Endpunkte – Übersicht

| Methode | Pfad | Scope | Zweck |
|---------|------|-------|-------|
| `GET`    | `/api/v1/data` | read | Gesamtes Dokument |
| `GET`    | `/api/v1/settings` | read | Globale + Pro-FY-Einstellungen |
| `GET`    | `/api/v1/projects` | read | Projekte (FY-gefiltert) |
| `GET`    | `/api/v1/projects/summary` | read | Berechnete Stunden je Projekt (Verbraucht/Rest/Auslastung) |
| `GET`    | `/api/v1/projects/{id}` | read | Einzelnes Projekt |
| `GET`    | `/api/v1/entries` | read | Einträge (gefiltert) |
| `GET`    | `/api/v1/goal` | read | Ziel-/Kapazitätsübersicht |
| `POST`   | `/api/v1/entries/sync` | write | Einträge upserten (Kern-Sync) |
| `POST`   | `/api/v1/projects` | write | Projekt anlegen |
| `PUT`    | `/api/v1/projects/{id}` | write | Projekt ändern |
| `DELETE` | `/api/v1/projects/{id}` | write | Projekt löschen |
| `PUT`    | `/api/v1/settings` | write | Globale Einstellungen ändern |
| `PUT`    | `/api/v1/settings/fiscal-years/{year}` | write | Pro-FY-Einstellungen ändern |

---

## Lesen

### `GET /api/v1/data`
Gibt das komplette Dokument zurück (`settings`, `fiscalYears`, `projects`,
`entries`). Der KI-API-Key wird immer entfernt.

```bash
curl -H "Authorization: Bearer $READ" https://host/api/v1/data
```

### `GET /api/v1/settings`
```json
{ "settings": { … }, "fiscalYears": { "2027": { … } } }
```

### `GET /api/v1/projects`
Query-Parameter:

| Parameter | Beschreibung |
|-----------|--------------|
| _(keiner)_ | nur Projekte des **aktiven** Fiskaljahres |
| `fiscalYear=YYYY` | Projekte des angegebenen FY |
| `all=true` | Projekte **aller** Fiskaljahre |

```bash
curl -H "Authorization: Bearer $READ" "https://host/api/v1/projects?fiscalYear=2027"
```
```json
{ "projects": [ { "id": "…", "name": "…", "budgetHours": 100, "fiscalYear": 2027, … } ] }
```

### `GET /api/v1/projects/summary`
Liefert die **berechneten** Stunden je Projekt für ein Fiskaljahr (Standard:
aktives FY, oder `?fiscalYear=YYYY`) – dieselben Zahlen wie die Projekte-Seite:
Budget, Forecast, Ist, **effektiv verbraucht** (Ist überschreibt Forecast pro
Tag) sowie Restbudget und Auslastung. Damit muss der Client die Einträge nicht
selbst aggregieren.

```bash
curl -H "Authorization: Bearer $READ" "https://host/api/v1/projects/summary"
```
```json
{
  "fiscalYear": 2027,
  "totalHours": 162,
  "projects": [
    {
      "id": "abc", "name": "Projekt A", "fiscalYear": 2027,
      "budgetHours": 200, "forecastHours": 120, "actualHours": 42,
      "consumedHours": 150, "remainingHours": 50, "utilizationPct": 75,
      "startDate": "2026-07-01", "endDate": "2027-06-30",
      "remainingWorkdays": 180, "requiredPerWorkday": 0.28, "outOfWindow": 0
    }
  ]
}
```
`consumedHours` ist die Summe aller Stunden (gebucht + Forecast), `forecastHours`
zählt die zukünftigen (ab heute), `actualHours` die vergangenen Tage,
`remainingHours` = `budgetHours − consumedHours`, `utilizationPct` =
`consumedHours / budgetHours × 100`.

### `GET /api/v1/projects/{id}`
Einzelnes Projekt oder `404`.

### `GET /api/v1/entries`
Query-Parameter (alle optional, kombinierbar):

| Parameter | Beschreibung |
|-----------|--------------|
| `from=YYYY-MM-DD` | nur Einträge ab (inkl.) diesem Datum |
| `to=YYYY-MM-DD`   | nur Einträge bis (inkl.) diesem Datum |
| `projectId=<id>`  | nur dieses Projekt |

```bash
curl -H "Authorization: Bearer $READ" \
  "https://host/api/v1/entries?from=2026-07-01&to=2026-07-31"
```
```json
{ "entries": [ { "date": "2026-07-01", "projectId": "…", "hours": 6 } ] }
```

### `GET /api/v1/goal`
Ziel-/Kapazitätsübersicht des aktiven FY, oder `?year=YYYY` für ein anderes FY.

---

## Schreiben

### `POST /api/v1/entries/sync` — Kern-Synchronisation

Upsert eines Stapels von Einträgen. Schlüssel je Eintrag: **(date, projectId)**.

- Existiert der Schlüssel bereits, werden die Stunden **überschrieben**, sonst **neu angelegt**.
- `hours: 0` **löscht** einen vorhandenen Eintrag (zum Leeren von Tagen).
- Einträge, die auf ein **unbekanntes Projekt** verweisen oder deren Datum
  **außerhalb des Buchungszeitraums** des Projekts liegt, werden übersprungen und
  gemeldet – der Rest des Stapels wird trotzdem angewendet.

**Request**
```json
{
  "entries": [
    { "date": "2026-07-01", "projectId": "abc", "hours": 6 },
    { "date": "2026-07-02", "projectId": "abc", "hours": 8 },
    { "date": "2026-07-03", "projectId": "abc", "hours": 0 }
  ]
}
```

**Response** `200`
```json
{ "upserted": 2, "deleted": 1, "skipped": [] }
```

`skipped` enthält bei Bedarf `{ "index": <n>, "reason": "<grund>" }` für jeden
verworfenen Eintrag (Index bezieht sich auf das gesendete Array).

```bash
curl -X POST https://host/api/v1/entries/sync \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"entries":[{"date":"2026-07-01","projectId":"abc","hours":6}]}'
```

> **Projekt-IDs**: Ein externes Tool ermittelt die IDs vorab über
> `GET /api/v1/projects`. IDs sind stabil und serverseitig vergeben.

### `POST /api/v1/projects` — anlegen

Die `id` wird **serverseitig** erzeugt und zurückgegeben.

| Feld | Pflicht | Standard |
|------|---------|----------|
| `name` | ja | – |
| `budgetHours` | nein | `0` |
| `color` | nein | zufällig aus Palette (`#rrggbb`) |
| `active` | nein | `true` |
| `fiscalYear` | nein | aktives FY |
| `startDate` / `endDate` | nein | leer (ganzes FY) |

```bash
curl -X POST https://host/api/v1/projects \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"name":"Neues Projekt","budgetHours":120,"fiscalYear":2027}'
```
**Response** `201` – das angelegte Projekt inkl. `id`.

### `PUT /api/v1/projects/{id}` — ändern

Teil-Update: nur mitgeschickte Felder (`name`, `budgetHours`, `color`, `active`,
`fiscalYear`, `startDate`, `endDate`) werden geändert. Das automatisch verwaltete
**Urlaubsprojekt** ist gesperrt → `409`. Unbekannte `id` → `404`.

```bash
curl -X PUT https://host/api/v1/projects/abc \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"budgetHours":150,"active":false}'
```

### `DELETE /api/v1/projects/{id}` — löschen

Löscht das Projekt **und alle zugehörigen Einträge**. Urlaubsprojekt → `409`,
unbekannte `id` → `404`.

```bash
curl -X DELETE https://host/api/v1/projects/abc -H "Authorization: Bearer $WRITE"
```
```json
{ "status": "gelöscht", "id": "abc" }
```

### `PUT /api/v1/settings` — globale Einstellungen

Teil-Update. Felder: `year` (aktives FY), `federalState` (Bundesland-Kürzel,
z. B. `BY`), `weeklyTargetHours`, `fiscalYearStartMonth` (1–12), `utilization`
(Ampel-Schwellen/Labels) und `ai` (`endpoint`/`deployment`/`apiVersion` –
**kein** Key). Ungültige Werte → `400`.

```bash
curl -X PUT https://host/api/v1/settings \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"federalState":"SN","weeklyTargetHours":40,"fiscalYearStartMonth":7}'
```

### `PUT /api/v1/settings/fiscal-years/{year}` — Pro-FY-Einstellungen

Teil-Update der FY-Werte: `targetHours`, `vacationDaysH1`, `vacationDaysH2`
(0–366), `standardTaskLabel`, `standardTaskHours`. Das **Urlaubsbudget** des
Urlaubsprojekts wird automatisch auf `(vacationDaysH1 + vacationDaysH2) × 8 h`
synchronisiert.

```bash
curl -X PUT https://host/api/v1/settings/fiscal-years/2027 \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"targetHours":1440,"vacationDaysH1":15,"vacationDaysH2":15,"standardTaskHours":250}'
```
```json
{ "fiscalYear": 2027, "settings": { … }, "vacationBudgetHours": 240 }
```

---

## Beispiel: Ist-Stunden vom Desktop synchronisieren

1. Projekte und deren IDs abrufen:
   ```bash
   curl -H "Authorization: Bearer $READ" "https://host/api/v1/projects?all=true"
   ```
2. Ist-Stunden der letzten Woche pushen (Upsert je Tag/Projekt):
   ```bash
   curl -X POST https://host/api/v1/entries/sync \
     -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
     -d '{"entries":[
       {"date":"2026-07-20","projectId":"abc","hours":8},
       {"date":"2026-07-21","projectId":"abc","hours":6},
       {"date":"2026-07-21","projectId":"xyz","hours":2}
     ]}'
   ```
3. Kontrolle:
   ```bash
   curl -H "Authorization: Bearer $READ" \
     "https://host/api/v1/entries?from=2026-07-20&to=2026-07-24"
   ```

Da der Sync idempotent ist, kann derselbe Zeitraum beliebig oft übertragen
werden – gleiche Schlüssel werden überschrieben, `hours=0` entfernt einen Tag.
