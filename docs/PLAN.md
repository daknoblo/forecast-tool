# Plan: forecast-tool (Go) as a Docker container

## Goal
A web-based, single-user forecast tool written in Go. Projects with an hour
budget, a per-day (Mon–Fri) hour forecast per project, weekly and yearly totals,
automatic public holidays (federal state selectable), configurable weekly target
hours, utilization in %, remaining budget and burn-down. Data lives in a JSON
file under `appdata/`. Built by GitHub Actions and pushed to GHCR, operated with
`docker compose` on another host.

## Decisions
- Stack: Go + `html/template` (standard library only, `net/http` ServeMux)
- Storage: a JSON file at `/appdata/data.json`, persisted through a compose volume
- Auth: none for the UI (single user, internal); bearer tokens for `/api/v1`
- Public holidays: automatic via `github.com/rickar/cal/v2` (DE, state selectable)
- Target working time: configurable per week (settings)
- Input: per day (Mon–Fri) and project; weekly totals computed automatically
- Budget view: remaining budget + burn-down chart (server-rendered SVG, offline capable)
- Charts: always server-rendered inline SVG — never a JavaScript charting dependency
- Deployment: `docker-compose.yml`, data in the `appdata` volume

## Data model (`data.json`)
- `Settings` (global): `year` (active fiscal year), `federalState` (default `SN`),
  `weeklyTargetHours` (default 40), `fiscalYearStartMonth`,
  `ai { endpoint, deployment, apiVersion }` (the API key is **not** in the file —
  it comes from `FORECAST_AI_API_KEY`), `utilization` (traffic-light thresholds
  and labels).
- `FiscalYears` (per FY): `targetHours`, `vacationDaysH1`/`vacationDaysH2`,
  `standardTaskLabel`, `standardTaskHours`. Defaults for new/unconfigured fiscal
  years (`models.DefaultFYSettings`): target 1440 h, vacation 15 days per half,
  standard tasks 250 h.
- `Project`: `id`, `assignmentId` (external assignment identifier, required for
  regular projects), `name`, `budgetHours`, `color`, `active`, `fiscalYear`,
  `startDate`, `endDate` (optional, ISO, inclusive = booking window; empty = the
  whole FY), `system` (`"vacation"` for the auto-managed vacation project).
- `Entry`: `date` (YYYY-MM-DD), `projectId`, `hours`. There is exactly **one**
  hours value per day and project; whether it counts as booked or forecast is
  derived from the date (past = booked, today and later = forecast). The legacy
  `kind` field is migration-only and is never written back.
- Public holidays are computed at runtime and never persisted.

## Project structure
```
cmd/server/main.go                  # bootstrap, HTTP server, graceful shutdown
internal/models/models.go           # Settings, AISettings, Project, Entry, Data, Validate
internal/models/util_test.go        # model unit tests
internal/storage/store.go           # JSON load/save, RWMutex, atomic write, ReplaceJSON/ValidateJSON/Mutate
internal/holidays/holidays.go       # wrapper around rickar/cal (DE states) + calendar cache
internal/ai/client.go               # Azure OpenAI-compatible AI client (JSON update by prompt, blueprint)
internal/ai/expand.go               # expands compact forecastPlan directives into Mon–Fri entries
internal/ai/expand_test.go          # expansion unit tests
internal/logging/logging.go         # slog logger: stdout + rotating file (10 MB) in appdata
internal/forecast/agg.go            # week/year aggregation, goal summary, burn-down, sankey
internal/forecast/agg_test.go       # aggregation unit tests
internal/api/api.go                 # /api/v1 routing, bearer-token auth, JSON helpers
internal/api/handlers.go            # /api/v1 read and write endpoints
internal/api/util.go                # API input helpers
internal/api/api_test.go            # API tests (httptest)
internal/web/handlers.go            # HTTP handlers + routing
internal/web/middleware.go          # security headers, same-origin guard, static asset versioning
internal/web/private.go             # private ("presentation") mode
internal/web/svg.go                 # burn-down, progress, sankey and free-capacity SVG generators
internal/web/util.go                # formatting/validation helpers
internal/web/web_test.go            # handler/middleware tests
internal/web/templates/*.html       # layout, dashboard, week, projects, goal, settings, JSON editor
internal/web/static/style.css       # styles
appdata/.gitkeep                    # data.json at runtime (git-ignored)
docs/PLAN.md                        # this plan
docs/API.md                         # HTTP API reference
Dockerfile                          # multi-stage, distroless, non-root, multi-arch
docker-compose.yml
.github/workflows/ci.yml            # CI: format, vet, lint, vulncheck, gosec, race tests, build
.github/workflows/release.yml       # verify -> container build + push -> GHCR (+ SBOM, cosign, Trivy)
.github/workflows/codeql.yml        # CodeQL analysis
go.mod
```

## Feature set
- **Dashboard**: KPI tiles (total budget, total forecast, projects, current FY
  week), a utilization Sankey diagram with a shiftable horizon, a free-capacity
  column chart on the same time axis, the budgets table
  (budget/forecast/booked/remaining/window/burn rate/utilization) and the weekly
  utilization table.
- **Multi-week forecast** (`/week`): projects × days across several weeks,
  holidays marked, one hours value per day, day/week totals, utilization against
  the weekly target, navigation. Buttons to clear single days or weeks, a
  traffic-light status row per week, and background auto-save via
  `POST /week/cells`.
- **Utilization traffic light**: globally configurable thresholds (min 26 /
  optimal 40 / over 60 h) and four freely editable labels. Coloured dots
  (blue ↓ / green OK / orange ↑ / red ✕) in the forecast, dashboard and goals
  tables.
- **Projects**: CRUD per fiscal year, budget, colour, active/inactive, start/end
  date (booking window), burn rate (h/week · h/day) plus required pace,
  remaining budget and a burn-down SVG. Projects sharing an `assignmentId` with
  an earlier fiscal year carry that year's consumed hours over, so the budget of
  a multi-year assignment is only granted once.
- **Goal/capacity**: FY target, vacation (per half-year), standard tasks,
  available hours, target per week/month/quarter, half-year and quarter roll-ups.
- **Settings**: FY start month, federal state (holidays), weekly target hours,
  per-FY values, utilization traffic light (`section=utilization`), AI endpoint
  (`section=ai`; endpoint, deployment, API version — the key comes from the
  environment) and an API-token status card. The data-file overview (path/size)
  sits at the very bottom.
- **Private mode**: a header toggle that masks every project name and figure,
  disables the JSON editor, the project forms and the forecast inputs. It is a
  per-browser display preference stored in a cookie, not in `data.json`.
- **Layout**: central app name via `web.AppName`/`{{appName}}` (title, header
  brand, footer). Navigation: Dashboard – Projekte – Forecast (`/week`) – Ziele
  (`/goal`) – JSON – Einstellungen. Footer with a link to the GitHub profile.
- **JSON editor** (`/data`): edit the whole data file in the browser, export it
  (`/export`), reset projects and bookings (`/data/reset`) and save with
  server-side validation; optionally update it with an AI prompt (`/data/ai`).
- **HTTP API** (`/api/v1`): token-protected read and write endpoints for external
  clients; see [API.md](API.md).

## Non-functional decisions
- **Concurrency**: the store is guarded by an `sync.RWMutex`; reads take a
  snapshot, writes go through `Update`/`Mutate` (copy → mutate → normalize →
  validate → persist), so a failed write never leaves a half-changed document.
- **Durability**: writes are atomic (temp file, `fsync`, rename) with `0600`
  permissions.
- **Caching**: holiday calendars are memoized process-wide; the two template sets
  (public/private) are cloned once at startup; static assets are served immutable
  with a content-hash query.
- **Security**: see the "Security model" section in the README.

## Verification
- `gofmt`, `go vet ./...`, `go build ./...` and `go test ./...` must be clean
- `docker build` succeeds; `docker compose up` serves the UI on `:8080`
- Manual: create a project, enter hours, see a holiday, check the burn-down
- The GitHub Actions run verifies, builds and pushes the multi-arch image to GHCR

## Possible future extensions
- Manual special days (vacation/sick) in addition to the automatic holidays
- CSV export of the yearly data
- Multi-year support / yearly archive
