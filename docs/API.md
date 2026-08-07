# forecast-tool – HTTP API

The JSON API under `/api/v1` lets external tools (e.g. a desktop client) **read**
and **synchronize** the forecast (hours per day/project), manage projects and
maintain settings.

The HTML UI intentionally stays unauthenticated (internal use only, behind a
reverse proxy). **Only `/api/**` is protected** — by two bearer tokens.

> **Language note:** the application's user interface is German, and so are the
> `error` messages returned by this API. Everything else in this repository is
> English.

---

## Authentication

Every API request must carry a bearer token in the `Authorization` header:

```
Authorization: Bearer <token>
```

There are two tokens, supplied **exclusively** through environment variables
(never in `data.json`, never logged):

| Environment variable         | Scope                     | Allowed methods                |
|------------------------------|---------------------------|--------------------------------|
| `FORECAST_API_READ_TOKEN`    | read                      | `GET`                          |
| `FORECAST_API_WRITE_TOKEN`   | read **and** write        | `GET`, `POST`, `PUT`, `DELETE` |

The write token includes read access. The **Settings** page of the web UI shows
whether the variables are set.

### Behaviour

- Missing or invalid token → `401 Unauthorized` (`WWW-Authenticate: Bearer`).
- Valid read token on a write endpoint → `403 Forbidden`.
- **Neither** token set → the API is disabled, every request → `503`.

> Tokens are compared in constant time. Use long, random values, e.g.
> `openssl rand -hex 32`.

---

## Conventions

- **Base URL:** `<host>/api/v1`
- **Content type:** request and response bodies are JSON (`application/json`).
- **Date format:** ISO `YYYY-MM-DD`.
- **Hours (`hours`):** decimal ≥ 0. There is exactly **one** value per day and
  project; whether it counts as **booked** or **forecast** follows from the date
  (past days = booked, today and later = forecast). There is **no** `kind` field.
- **Error format:** `{ "error": "<German message>" }` with a matching HTTP status.
- **Request body limit:** 2 MiB. Unknown JSON fields are rejected (`400`).
- **Partial updates (`PUT`):** only the fields you send are changed. For the
  project date fields, `""` means "clear the window" and omitting the field means
  "leave unchanged".

### Status codes

| Code  | Meaning |
|-------|---------|
| `200` | OK |
| `201` | Resource created |
| `400` | Invalid input (body, parameter, validation) |
| `401` | Token missing or invalid |
| `403` | Read token used on a write endpoint |
| `404` | Resource not found |
| `409` | Conflict (e.g. the auto-managed vacation project) |
| `503` | API disabled (no tokens configured) |

---

## Endpoint overview

| Method   | Path | Scope | Purpose |
|----------|------|-------|---------|
| `GET`    | `/api/v1/data` | read | Whole document |
| `GET`    | `/api/v1/settings` | read | Global + per-FY settings |
| `GET`    | `/api/v1/projects` | read | Projects (filtered by FY) |
| `GET`    | `/api/v1/projects/summary` | read | Computed hours per project (consumed/remaining/utilization) |
| `GET`    | `/api/v1/projects/{id}` | read | Single project |
| `GET`    | `/api/v1/entries` | read | Entries (filtered) |
| `GET`    | `/api/v1/goal` | read | Goal/capacity summary |
| `POST`   | `/api/v1/entries/sync` | write | Upsert entries (the core sync) |
| `POST`   | `/api/v1/projects` | write | Create a project |
| `PUT`    | `/api/v1/projects/{id}` | write | Update a project |
| `DELETE` | `/api/v1/projects/{id}` | write | Delete a project |
| `PUT`    | `/api/v1/settings` | write | Update global settings |
| `PUT`    | `/api/v1/settings/fiscal-years/{year}` | write | Update per-FY settings |

> The web UI additionally exposes `GET /healthz` (liveness probe) and
> `GET /export` (JSON download). Both live outside `/api/v1` and need no token.

---

## Reading

### `GET /api/v1/data`
Returns the complete document (`settings`, `fiscalYears`, `projects`,
`entries`). The AI API key is always redacted.

```bash
curl -H "Authorization: Bearer $READ" https://host/api/v1/data
```

### `GET /api/v1/settings`
```json
{ "settings": { … }, "fiscalYears": { "2027": { … } } }
```

### `GET /api/v1/projects`
Query parameters:

| Parameter | Description |
|-----------|-------------|
| _(none)_  | only projects of the **active** fiscal year |
| `fiscalYear=YYYY` | projects of the given FY |
| `all=true` | projects of **all** fiscal years |

```bash
curl -H "Authorization: Bearer $READ" "https://host/api/v1/projects?fiscalYear=2027"
```
```json
{ "projects": [ { "id": "…", "assignmentId": "5641245", "name": "…", "budgetHours": 100, "fiscalYear": 2027, … } ] }
```

### `GET /api/v1/projects/summary`
Returns the **computed** hours per project for a fiscal year (default: the
active FY, or `?fiscalYear=YYYY`) — the same figures the projects page shows:
budget, cross-fiscal-year carry-over, forecast, booked, consumed, remaining
budget, burn rate and utilization. This saves the client from aggregating the
entries itself.

```bash
curl -H "Authorization: Bearer $READ" "https://host/api/v1/projects/summary"
```
```json
{
  "fiscalYear": 2027,
  "totalHours": 162,
  "totalCarryOver": 40,
  "projects": [
    {
      "id": "abc", "assignmentId": "5641245", "name": "Projekt A", "fiscalYear": 2027,
      "budgetHours": 200, "carryOverHours": 40, "futureFyHours": 0,
      "availableBudgetHours": 160,
      "forecastHours": 120, "actualHours": 42,
      "consumedHours": 162, "remainingHours": -2, "utilizationPct": 101,
      "startDate": "2026-07-01", "endDate": "2027-06-30",
      "burnPerWeek": 3.2, "burnPerWorkday": 0.6,
      "remainingWorkdays": 180, "requiredPerWorkday": 0, "outOfWindow": 0
    }
  ]
}
```

> `totalHours` and `totalCarryOver` sum the **assignments** of the fiscal year;
> the auto-managed vacation project is not part of them (it has no assignment and
> never counts towards the goal). It does appear in `projects` with its own row.

| Field | Meaning |
|-------|---------|
| `carryOverHours` | hours of the same assignment dated in **earlier** fiscal years |
| `futureFyHours` | hours of the same assignment dated in **later** fiscal years |
| `availableBudgetHours` | `budgetHours − carryOverHours` — what is still available in this FY |
| `forecastHours` | hours on today and future days, inside this fiscal year |
| `actualHours` | hours on past days (booked), inside this fiscal year |
| `consumedHours` | all hours dated **inside** this fiscal year: `forecastHours + actualHours` |
| `remainingHours` | `availableBudgetHours − consumedHours` |
| `utilizationPct` | `(carryOverHours + consumedHours) / budgetHours × 100` |
| `burnPerWeek` / `burnPerWorkday` | `availableBudgetHours` spread evenly over the working days of the booking window |
| `remainingWorkdays` | working days (Mon–Fri minus holidays) from today to the window end |
| `requiredPerWorkday` | `remainingHours / remainingWorkdays` — what is still left to plan per day |
| `outOfWindow` | hours booked inside the fiscal year but outside the project's booking window |

> **Assignments across fiscal years:** a project belongs to exactly one fiscal
> year. An assignment that keeps running is re-created in the new FY with the
> **same `assignmentId`** and the assignment's total budget. Hours are then
> attributed to a fiscal year by their **date**, not by the project row they were
> booked on: with a July start, everything up to 30 June counts towards the old
> fiscal year and everything from 1 July towards the new one. The earlier years'
> hours are reported as `carryOverHours` and deducted from the budget, so budget
> is never granted twice; hours that already lie in a later fiscal year appear as
> `futureFyHours`.

### `GET /api/v1/projects/{id}`
A single project, or `404`.

### `GET /api/v1/entries`
Query parameters (all optional, combinable):

| Parameter | Description |
|-----------|-------------|
| `from=YYYY-MM-DD` | only entries on or after this date |
| `to=YYYY-MM-DD`   | only entries on or before this date |
| `projectId=<id>`  | only this project |

```bash
curl -H "Authorization: Bearer $READ" \
  "https://host/api/v1/entries?from=2026-07-01&to=2026-07-31"
```
```json
{ "entries": [ { "date": "2026-07-01", "projectId": "…", "hours": 6 } ] }
```

### `GET /api/v1/goal`
Goal/capacity summary of the active FY, or `?year=YYYY` for another one.

---

## Writing

### `POST /api/v1/entries/sync` — the core synchronization

Upserts a batch of entries. The key of each item is **(date, projectId)**.

- If the key already exists the hours are **overwritten**, otherwise a new entry
  is **created**.
- `hours: 0` **deletes** an existing entry (to clear a day).
- Items referring to an **unknown project** are skipped and reported — the rest
  of the batch is still applied.
- A date **outside the project's booking window** is accepted: such entries can be
  created, edited and deleted like any other. The window only drives the
  `outOfWindow` warning and the visual hint in the forecast grid.

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

When needed, `skipped` contains `{ "index": <n>, "reason": "<reason>" }` for
every discarded item (the index refers to the array you sent).

```bash
curl -X POST https://host/api/v1/entries/sync \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"entries":[{"date":"2026-07-01","projectId":"abc","hours":6}]}'
```

> **Project IDs**: an external tool resolves the IDs up front via
> `GET /api/v1/projects`. IDs are stable and assigned server-side.

### `POST /api/v1/projects` — create

The `id` is generated **server-side** and returned.

| Field | Required | Default |
|-------|----------|---------|
| `name` | yes | – |
| `assignmentId` | yes | – |
| `budgetHours` | no | `0` |
| `color` | no | random from the palette (`#rrggbb`) |
| `active` | no | `true` |
| `fiscalYear` | no | active FY |
| `startDate` / `endDate` | no | empty (whole FY) |

```bash
curl -X POST https://host/api/v1/projects \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"name":"Neues Projekt","assignmentId":"5641245","budgetHours":120,"fiscalYear":2027}'
```
**Response** `201` — the created project including its `id`.

### `PUT /api/v1/projects/{id}` — update

Partial update: only the fields you send (`name`, `assignmentId`, `budgetHours`,
`color`, `active`, `fiscalYear`, `startDate`, `endDate`) are changed. For the
**vacation project** `budgetHours` is ignored (it is derived from the vacation
days in the FY settings); every other field is editable. Unknown `id` → `404`.

```bash
curl -X PUT https://host/api/v1/projects/abc \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"budgetHours":150,"active":false}'
```

### `DELETE /api/v1/projects/{id}` — delete

Deletes the project **and all of its entries**. Vacation project → `409`,
unknown `id` → `404`.

```bash
curl -X DELETE https://host/api/v1/projects/abc -H "Authorization: Bearer $WRITE"
```
```json
{ "status": "gelöscht", "id": "abc" }
```

### `PUT /api/v1/settings` — global settings

Partial update. Fields: `year` (active FY), `federalState` (state code, e.g.
`BY`), `weeklyTargetHours`, `fiscalYearStartMonth` (1–12), `utilization`
(traffic-light thresholds/labels) and `ai` (`endpoint`/`deployment`/`apiVersion`
— **no** key). Invalid values → `400`.

```bash
curl -X PUT https://host/api/v1/settings \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"federalState":"SN","weeklyTargetHours":40,"fiscalYearStartMonth":7}'
```

### `PUT /api/v1/settings/fiscal-years/{year}` — per-FY settings

Partial update of the FY hour configuration: `weekdayHours` (gross FY hours;
`0` keeps the calendar-derived value), `vacationDays` (0–366), `holidayDays`
(0–366; omit to keep the federal state's calendar), `standardTaskLabel`,
`standardTaskHours`. The **vacation budget** of the vacation project is
automatically synchronized to `vacationDays × 8 h`.

There is **no `targetHours`**: the fiscal-year goal is the net of this
breakdown (gross − vacation − holidays − standard tasks).

The deprecated keys `vacationDaysH1`/`vacationDaysH2` are still accepted and
summed into `vacationDays`.

```bash
curl -X PUT https://host/api/v1/settings/fiscal-years/2027 \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"vacationDays":30,"standardTaskHours":250}'
```
```json
{ "fiscalYear": 2027, "settings": { … }, "vacationBudgetHours": 240 }
```

---

## Example: synchronizing booked hours from the desktop

1. Fetch the projects and their IDs:
   ```bash
   curl -H "Authorization: Bearer $READ" "https://host/api/v1/projects?all=true"
   ```
2. Push last week's booked hours (upsert per day/project):
   ```bash
   curl -X POST https://host/api/v1/entries/sync \
     -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
     -d '{"entries":[
       {"date":"2026-07-20","projectId":"abc","hours":8},
       {"date":"2026-07-21","projectId":"abc","hours":6},
       {"date":"2026-07-21","projectId":"xyz","hours":2}
     ]}'
   ```
3. Verify:
   ```bash
   curl -H "Authorization: Bearer $READ" \
     "https://host/api/v1/entries?from=2026-07-20&to=2026-07-24"
   ```

Because the sync is idempotent, the same period can be transferred as often as
you like — identical keys are overwritten and `hours=0` removes a day.
