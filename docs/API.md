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
  "leave unchanged". Exception: the Forecast Accuracy endpoint replaces one
  complete snapshot and requires both `percentage` and `asOf`.

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
| `GET`    | `/api/v1/forecast-accuracy/{year}` | read | Imported ESXP accuracy and FY-end worst case |
| `PUT`    | `/api/v1/forecast-accuracy/{year}` | write | Replace one FY's accuracy snapshot |
| `POST`   | `/api/v1/entries/sync` | write | Upsert entries (the core sync) |
| `POST`   | `/api/v1/projects` | write | Create a project |
| `PUT`    | `/api/v1/projects/{id}` | write | Update a project |
| `DELETE` | `/api/v1/projects/{id}` | write | Delete a project |
| `PUT`    | `/api/v1/settings` | write | Update global settings |
| `PUT`    | `/api/v1/settings/fiscal-years/{year}` | write | Update per-FY settings |

> The web UI additionally exposes `GET /healthz` (liveness probe) and
> `GET /export` (JSON download). Both live outside `/api/v1` and need no token.

---

## ESXP Forecast Accuracy

### `PUT /api/v1/forecast-accuracy/{year}`

The ESXP reader sends the displayed **percentage for that fiscal year**, not
individual weekly comparisons. This repository does not scrape ESXP. Extend
the external reader to call this endpoint with the write bearer token:

```http
PUT /api/v1/forecast-accuracy/2027
Authorization: Bearer <FORECAST_API_WRITE_TOKEN>
Content-Type: application/json

{"percentage":100,"asOf":"2026-09-24"}
```

Replace the example FY and date with those of the actual observation. `asOf`
is the UTC reference date of the observed percentage, **not** an arbitrary
upload date for an older observation. Both fields are required. `percentage`
is a number in `[0,100]` (not a fraction); zero is valid, null is not.
`asOf` must be `YYYY-MM-DD`, on or after the FY start and not in the future.
Observations after the FY ends are allowed for finalized annual values.
Years must be in `[2000,2100]`.

The endpoint replaces that FY's snapshot atomically without changing entries,
projects, settings, or the active FY. Same-date corrections are allowed;
an observation older than the saved `asOf` returns **409** without changes.
Invalid input returns **400**, persistence failures **500**.
The configured FY start month is captured alongside the value. A later change
to the FY calendar disables the projection until a matching ESXP value is
imported again. Storage and JSON exports contain:

```json
"forecastAccuracy": {
  "2027": {
    "percentage": 100,
    "asOf": "2026-09-24",
    "fiscalYearStartMonth": 7
  }
}
```

### `GET /api/v1/forecast-accuracy/{year}`

GET and successful PUT return the same computed summary. For example, reading
the observation above in the same week returns:

```json
{
  "fiscalYear": 2027,
  "hasData": true,
  "hasProjection": true,
  "percentage": 100,
  "minimumPct": 22.641509433962263,
  "drawdownPoints": 77.35849056603774,
  "asOf": "2026-09-24",
  "evaluatedWeeks": 12,
  "remainingWeeks": 41,
  "totalWeeks": 53,
  "stale": false
}
```

- ESXP is authoritative: a week is correct when actual and forecast hours
  differ by **at most 8 hours in either direction**. This app does not
  reconstruct the imported rate from its mutable daily entries.
- The denominator of the imported rate is every **completed FY week at
  `asOf`**. The current Monday-based week is excluded. Partial boundary weeks
  count like the existing forecast's FY weeks; the final partial week is
  complete on the first day after FY end. Total FY weeks may be 53 or 54;
  they are not hard-coded to 52.
- `minimumPct = percentage × evaluatedWeeks / totalWeeks`, assuming every
  remaining week is incorrect. `drawdownPoints = percentage − minimumPct`,
  measured in **percentage points**, not relative percent.
- `stale` becomes true when more FY weeks have completed since `asOf`.
  The calculation stays anchored to that observation, conservatively assuming
  **all unassessed weeks since then** fail, rather than silently applying an old
  rate to a new denominator. Sync a fresh observation to calculate the
  worst case starting with the actual current week.
- Missing data: `hasData:false`. No completed weeks, missing data, or a changed
  FY calendar: `hasProjection:false`. Ignore the corresponding numeric fields
  when these flags are false; zero is not a fabricated accuracy or projection.
  Invalid stored data or a calendar mismatch also includes a German `error`.
- ESXP's source precision limits the projection's precision. Inferred
  successful weeks are **not rounded to integers**.
- The dashboard shows the rate, minimum and observation date. Private mode
  replaces imported values with fictional sample data.

## Reading

### `GET /api/v1/data`
Returns the complete document (`settings`, `fiscalYears`, `projects`,
`entries`, and optional `savedMonthPlans`/`forecastAccuracy`). The AI API key is always redacted. Foundry identity, client secrets
and discovered catalogs are environment/runtime configuration, not part of this
document. In Foundry mode `settings.ai.deployment` selects a discovered chat
deployment; the manual endpoint and API version are ignored for inference.
Unknown or incompatible deployments cannot be used for chat.

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
      "releasedHours": 0, "availableBudgetHours": 160,
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
| `releasedHours` | unplanned budget an **inactive** project gives up (`0` while `active`) |
| `availableBudgetHours` | `budgetHours − carryOverHours − releasedHours` — what is still available in this FY |
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

> **Inactive projects** (`"active": false`, also settable through
> `PUT /api/v1/projects/{id}`) are treated as finished: every booked and forecast
> hour stays, but the budget that was never planned is assumed to never be called
> off. It is reported as `releasedHours` and removed from
> `availableBudgetHours`, `remainingHours` and the burn rate.

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
`BY`), `weeklyTargetHours`, `fiscalYearStartMonth` (1–12), `dashboardRange`
(`1w`, `2w`, `4w`, `2m`, `3m`, `6m`, `fy`; default `4w`), `monthPlanningPrompt`,
`monthPlanningSystemPrompt`, `utilization`
(traffic-light thresholds/labels) and `ai` (`endpoint`/`deployment`/`apiVersion`
— **no** key). Invalid values → `400`.

`dashboardRange` controls the dashboard charts when no valid `sankey` query
parameter is supplied. Explicit horizon links do not change the saved default.

`monthPlanningPrompt` is the editable task prompt for monthly AI planning;
`monthPlanningSystemPrompt` is the editable system prompt and response format.
Both are persisted in the existing data document (maximum 8,000 Unicode
characters each). Empty/whitespace selects the respective built-in prompt;
omission keeps the existing value. Changing either does not generate or save a
forecast. Server-side plan validation cannot be overridden by either prompt:
historical-only projects cannot acquire new forecast hours, and existing totals
must remain unchanged per project/week.

The full data document's `savedMonthPlans` map (`YYYY-MM` to RFC3339 timestamp)
is maintained when a monthly preview is explicitly saved. It selects exact
stored-day rendering instead of local re-estimation; it is not an alternate
entry store. It is not writable through this settings endpoint. Generation,
ephemeral previews and explicit saves belong to the same-origin HTML UI,
not the bearer-authenticated JSON API.

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
