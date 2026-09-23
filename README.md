# forecast-tool

[![CI](https://github.com/daknoblo/forecast-tool/actions/workflows/ci.yml/badge.svg)](https://github.com/daknoblo/forecast-tool/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/daknoblo/forecast-tool)](https://github.com/daknoblo/forecast-tool/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/daknoblo/forecast-tool)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![GHCR](https://img.shields.io/badge/ghcr.io-forecast--tool-blue?logo=docker)](https://github.com/daknoblo/forecast-tool/pkgs/container/forecast-tool)

A lightweight single-user **forecast tool** written in Go, with a server-rendered
web interface. Create projects with an hour budget, forecast hours per project
per day (Mon–Fri) and keep track of weekly/yearly totals, utilization and
remaining budget. Planning is **fiscal-year based** (the start month is
configurable). Public holidays are applied automatically per German federal
state.

Data lives in a single JSON file under `appdata/data.json` — no database
required. The file can be exported at any time and analysed through an AI
prompt on the goals page.

> **Note on language:** the user interface is intentionally German. Everything
> else in this repository (code, comments, documentation) is English.

## Demo & screenshots
**[→ Live demo and documentation site](https://daknoblo.github.io/forecast-tool/)**

The site is rebuilt on every push to `main`: the application is started with a
generated demo data set, every page is captured as a **clickable static
snapshot**, the screenshots below are taken automatically and the Markdown files
of this repository are rendered alongside them. So the documentation, the demo
and the screenshots can never drift away from the code.

| | |
|---|---|
| [![Dashboard](https://daknoblo.github.io/forecast-tool/screenshots/dashboard.png)](https://daknoblo.github.io/forecast-tool/screenshots.html#dashboard.png) | [![Forecast grid](https://daknoblo.github.io/forecast-tool/screenshots/forecast.png)](https://daknoblo.github.io/forecast-tool/screenshots.html#forecast.png) |
| **Dashboard** – KPI tiles, utilization Sankey, free capacity, budgets | **Forecast grid** – projects × days, one hours value per day, auto-saved |
| [![Goals](https://daknoblo.github.io/forecast-tool/screenshots/goal.png)](https://daknoblo.github.io/forecast-tool/screenshots.html#goal.png) | [![Projects](https://daknoblo.github.io/forecast-tool/screenshots/projects.png)](https://daknoblo.github.io/forecast-tool/screenshots.html#projects.png) |
| **Goals & capacity** – derived FY target, hours flow, progress charts | **Projects** – budget, carry-over, burn rate, burn-down |

Building the site locally:
```bash
(cd tools/screenshots && npm ci && npx playwright install chromium)
go run ./cmd/docsite -out site
# then open site/index.html
```
`go run ./cmd/docsite -out site -screenshots=false` skips the browser step and
only renders the docs and the demo snapshot.

## Features
- Projects with an hour budget (CRUD, colour, active/inactive), scoped to a
  fiscal year, each with a unique **assignment ID** (required on creation,
  e.g. `5641245`)
- **Closing a project**: switching it to inactive keeps every booked and forecast
  hour, but releases the budget that was never planned — it drops out of the
  available budget, the remainder and the burn rate
- **Assignments across fiscal years**: a project belongs to exactly one fiscal
  year, so a continuing assignment is re-created in the new year with the same
  assignment ID and the assignment's total budget. Hours are attributed to a
  fiscal year by their **date** — with a July start everything up to 30 June
  counts towards the old year and everything from 1 July towards the new one.
  The earlier years' hours are deducted automatically as a **carry-over**, so
  the remaining budget and the burn rate never hand out budget twice, and the
  projects page shows the full per-fiscal-year split of the assignment
- **Per-project booking window** (optional start/end date): a planning hint, not
  a lock — days outside it stay editable in the forecast grid and via the API and
  are only marked. From it the tool derives the **burn rate** (h/week and
  h/working day), the pace still required, and a warning when hours were booked
  outside the window
- Multi-week forecast grid: projects × days (Mon–Fri) across several weeks with
  **one hours value per day** (past days count as booked, today and later as
  forecast), automatic totals, and buttons to clear single days or whole weeks
- **Auto-save**: edits in the forecast grid are persisted in the background
  (`POST /week/cells`); the page is never reloaded while typing
- **Activity indicator**: the always-visible ring before “Privat” in the header
  animates during data requests, saves and same-tab navigation. It covers monthly
  AI planning, goal chat, forecast cells, settings/project autosaves and form
  actions such as deployment refresh. Concurrent requests keep it active until
  all finish; errors retain their existing messages. Reduced-motion preferences
  disable rotation while the active colour still indicates pending work.
- **Monthly planning** (`/month`): a Monday–Friday calendar beside Forecast,
  with public holidays, vacation, project blocks and weekly capacity balances.
  Past days always show stored entries; today and future days initially show a
  **read-only local estimate**, without a view switch. Compact project tiles
  place names and hours on one line; each day header combines weekday/date,
  hours/capacity and a yellow overtime badge.
  The title, month navigation/Today and AI planning button share one header row
  on wide screens, without subtitles (controls wrap on narrow screens).
  The weekly sidebar lists net capacity, **Urlaub/Feiertage**, each project's weekly hours
  with its project colour, total booked hours and available capacity.
  The absence row uses the active fiscal year's configured vacation-project colour.
  Totals
  include adjacent-month days within the FY, weekend entries and unallocated
  hours. A yellow overload badge appears beside the week identifier only when
  the weekly total exceeds capacity; there is no date-range line underneath.
  Fiscal-week identifiers use **FYW10** throughout the UI; **KW** continues to
  identify ISO calendar weeks.
  **In monthly planning only**, vacation is an absence, not booked work.
  Daily and weekly booked totals exclude vacation; net capacity subtracts
  weekday public holidays and vacation (partial days proportionally), at most
  8 hours per day, without double-counting overlapping absences or weekends.
  The combined absence row shows this capacity reduction. For example, one
  vacation day plus one weekday public holiday leave 24 hours of a 40-hour week.
  Vacation entries remain visible and unchanged in storage. Forecast, dashboard
  and goal calculations retain their existing semantics.
  Existing weekly project totals are distributed using weekday patterns from the
  last 12 completed weeks (matched across FYs by assignment ID). Newer weeks
  carry more weight (four-week half-life); normalized weekly shares keep one
  unusually large booking from dominating. Historical holiday/vacation
  availability is accounted for. Fewer than four observed project weeks blends
  the project pattern with the personal pattern across all projects, itself
  blended with an even split when history is sparse. No history means an even
  capacity-weighted split.
  Past bookings and vacation never move. Suggestions respect project windows,
  holidays and the existing 8-hour workday capacity, reduced by partial vacation.
  Narrow project windows are allocated first, then concentrated weekday patterns
  (ties by project ID); remaining
  hours use other available weekdays when historical preferences cannot fit.
  Overflow is listed explicitly as unallocated, never lost or moved to another
  week/FY. Whole boundary weeks include adjacent-month dates within the active FY.
  Weekends have no columns; any stored weekend hours remain in weekly totals
  and are explicitly noted in the weekly summary.
  Standard tasks have no daily dates and are not deducted separately.
  The local estimate does not write data or call an external service.
  **“Planung mit KI regenerieren”** explicitly sends the last 84 days of actual
  daily bookings, project/assignment metadata, weekly forecast totals and
  holiday/vacation availability to the configured AI deployment. The editable,
  globally saved planning prompt under **“Wie entsteht die Schätzung?”** asks for real
  booking patterns such as four 2-hour blocks rather than five 1.6-hour blocks.
  Both the planning prompt and system prompt/response format are editable there,
  saved in the existing data document as `settings.monthPlanningPrompt` and
  `settings.monthPlanningSystemPrompt` (8,000 characters each; empty uses the
  respective built-in default). Changes apply to new previews, not saved entries.
  History also includes occasional projects without forecast hours; it supplies
  patterns only, never new planning totals. Only positive `editableHours` for the
  exact project/week permit new allocations.
  Strict server-side validation, independent of edited prompts, enforces project/week
  totals, project windows, holidays and full-day vacation. Eight hours remains the
  regular capacity, **not a hard AI planning limit**. Actual historical overtime
  is included in the context. A reference derived from the 90th percentile of
  positive daily project totals on non-holiday weekdays without vacation
  (at least 8 hours; 8 hours without history) guides plausible distributions.
  The AI context includes the observation count, median and reference, reduced by
  partial vacation. Higher values remain allowed and are shown directly on the
  calendar days; the preview has no duplicate workload summary or confirmation
  checkbox. Clicking **“Forecast speichern”** is sufficient for a valid complete
  plan. This is not a legal working-time approval.
  The local non-AI estimator and regular capacity/overtime indicators
  retain their existing 8-hour baseline. No credentials or unrelated
  settings are part of the planning context.
  Results first appear as an **unsaved preview**.
  The explanation renders Markdown paragraphs, lists and emphasis.
  Every request adds a presentation instruction for one bullet per project with
  a bold project name, including when custom prompts are saved. This instruction
  is visible in the prompt details and does not modify those saved prompts.
  Raw HTML is disabled; links and images are rendered as text without loading
  external resources. Existing plain-text explanations remain readable.
  Only **“Forecast speichern”**
  replaces future non-vacation entries in the displayed month, including today.
  Unlike the local estimate, AI planning never moves hours into adjacent months.
  Past entries, vacation and out-of-month dates remain unchanged. Unallocated
  hours block saving; invalid or truncated responses are rejected without a
  fallback write. Drafts expire after 30 minutes or a restart; changed source
  data requires regeneration. Saved months render their exact stored distribution
  even after reload (later manual edits remain visible), without local
  redistribution. Prompt editing, generation and saving are blocked in private
  mode. No clock times or additional forecast hours are created.
- Configurable **utilization traffic light**: four states (minimum burn rate,
  optimal, too high, overbooked) with freely chosen thresholds (hours) and
  labels; coloured dots in the forecast grid and in the weekly tables of the
  dashboard and goals pages. Both pages use the same weekly utilization table
  (week and date range, target hours, booked/planned hours, status and utilization)
- Fiscal-year logic (configurable start month) with a central FY switcher in the
  header
- Automatic public holidays (all 16 German federal states)
- Configurable weekly target hours, vacation, public holidays and standard tasks
- Goal/capacity calculation: the **fiscal-year goal is derived** from the hour
  configuration (gross FY hours − vacation − holidays − standard tasks), with
  target per week/month/quarter, an **hours-flow Sankey** (projects → months →
  quarters → half-years → fiscal year) and a progress chart per half-year and
  quarter. All goal charts mark the target crossing with an approximate date
  interpolated from the displayed monthly curve, or indicate that target
  attainment cannot currently be estimated when the curve does not cross it.
- Remaining budget per project plus a burn-down chart (server-rendered SVG)
- Dashboard with a **utilization Sankey diagram** and a **free capacity** chart,
  both rendered server-side as inline SVG (no JavaScript dependencies). Every
  horizon — from a single week up to the whole fiscal year — is bucketed by ISO
  week, because the weekly rate is what drives the forecast accuracy.
  Choose the **default dashboard horizon** under *Einstellungen → Global*
  (initially four weeks); dashboard horizon links override it temporarily.
- **Week-to-date** tile: the utilization reached since the fiscal year started —
  the hours actually booked against the FY goal spread evenly over the year's
  weeks. Working 40 h where the even split only asks for 27.7 h shows as ~147 %
  (today is excluded while it is still running, vacation does not count)
- **Working time per Werktag (§3 ArbZG)**: a split dashboard tile with the
  rolling 6-month average — the balancing period the law names — booked and
  planned side by side, plus a timeline on the goal page centred on today: six
  months of history to the left, the forecast to the right for as far as it
  reaches (at most eight months), against the 8 h average and the 10 h
  single-day cap. The columns are single months, the line on top is the rolling
  six-month average — the figure that actually has to stay under the limit — and
  it continues into the forecast, so you can see whether the plan brings the
  balance back. Werktage are Monday to Saturday without public holidays;
  vacation is left out entirely so it neither lifts nor lowers the average
- **Private mode**: a header toggle that swaps the real document for a complete
  set of sample data — every chart, bar and indicator stays alive, nothing real
  is on screen. Useful when sharing a screen
- **Chat with your data**: ask questions about the fiscal year in plain language
  against a configurable, Azure OpenAI-compatible endpoint (e.g. an Azure AI
  Foundry model router); only a compact summary is sent, never the raw file
- **Export** the whole data file as JSON from the settings page
- **HTTP JSON API** under `/api/v1` for external clients, protected by bearer tokens
- Data stored as JSON in a volume; runs as a small distroless container

### Defaults for new data
On first start (empty data file) and for fiscal years that have not been
configured yet, the following defaults apply:

| Setting                     | Default          |
|-----------------------------|------------------|
| Federal state (holidays)    | `SN` (Saxony)    |
| Weekly target hours         | `40`             |
| Gross FY hours              | weekdays × 8 h   |
| Vacation                    | `30` days        |
| Public holidays             | from the state   |
| Standard tasks              | `250` h          |

The fiscal-year goal is not configured separately: it is what remains of the
gross FY hours after vacation, public holidays and standard tasks.

## Quick start (local, with Go)
```bash
go run ./cmd/server
# UI: http://localhost:8080
```

Configuration via environment variables:

| Variable                    | Default   | Description                                              |
|-----------------------------|-----------|----------------------------------------------------------|
| `FORECAST_ADDR`             | `:8080`   | Listen address                                           |
| `FORECAST_DATA_DIR`         | `appdata` | Directory holding `data.json`                            |
| `PORT`                      | `8080`    | Port (alias, used when `FORECAST_ADDR` is unset)         |
| `DATA_DIR`                  | `appdata` | Data directory (legacy alias for `FORECAST_DATA_DIR`)    |
| `FORECAST_AI_API_KEY`       | –         | Secret API key for the AI endpoint (never stored in `data.json`) |
| `FORECAST_API_READ_TOKEN`   | –         | Bearer token for **read** access (GET) to the JSON API (`/api/v1`) |
| `FORECAST_API_WRITE_TOKEN`  | –         | Bearer token for **read and write** access to the JSON API |

## Security model
The application is designed to run on a private network behind a reverse proxy
(Traefik, optionally plus an identity-aware proxy). The HTML UI therefore has
**no authentication of its own**; the following measures harden it regardless:

- **`/api/v1` requires a bearer token** (read-only or read+write). With neither
  token configured the API is disabled and answers `503` (fail-closed). Tokens
  are compared in constant time and are never stored or logged.
- **Same-origin enforcement** on every state-changing UI request (CSRF defence):
  requests a browser reports as cross-site are rejected with `403`.
- **Security response headers** on every reply: `Content-Security-Policy`
  (same-origin only, no framing, no plugins), `X-Content-Type-Options`,
  `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`.
- **Secrets only via environment variables** — never in `data.json`, never logged.
- The AI client **refuses HTTP redirects**, so the API key cannot be forwarded to
  a third-party host.
- Strict input validation on every write path; the whole document is validated
  before it is persisted, so a bad payload can never corrupt the store.
- The container runs **non-root (UID 65532)**, read-only, with all capabilities
  dropped and `no-new-privileges`.

## Chat with your data
At the bottom of the **Ziele** page you can ask questions about your own
figures. Pick one of the ready-made prompts from the drop-down (e.g. "Fasse
meine Projekte für dieses Jahr zusammen") or type your own — the preset only
fills the input, so it stays editable.

The browser sends nothing but the question. The server builds a compact,
factual digest of the active fiscal year (totals against the goal, capacity,
per-project budgets with booked/forecast/remaining hours, hours per month,
quarter and per project and month) and sends that together with the question.
The raw data file never leaves the machine, and the section is disabled while
**private mode** is on.

Monthly AI planning uses the same deployment and authentication, but sends the
selected planning context including individual historical daily bookings rather
than the goal chat's aggregated digest. It has a separate editable prompt and an
explicit preview/save workflow; the goal chat remains read-only.

### Foundry with Microsoft Entra ID

For automatic endpoint and deployment discovery, configure these environment
variables and restart the server:

| Environment variable | Purpose |
|----------------------|---------|
| `AZURE_RESOURCE_ID` | Full account resource ID: `/subscriptions/<subscription-id>/resourceGroups/<group>/providers/Microsoft.CognitiveServices/accounts/<account>` |
| `AZURE_TENANT_ID` | Entra tenant ID |
| `AZURE_CLIENT_ID` | Application/client ID |
| `AZURE_CLIENT_SECRET` | Application client secret, supplied only through the environment |

The compose files pass these variables through from the host or `.env` (see
`.env.example`). Use the **account** resource ID, not a Foundry project URL.
Grant the application resource/deployment read permissions (for example, Reader
on that account) and inference permissions for the intended models (for Azure
OpenAI, for example Cognitive Services OpenAI User).

**Settings → AI endpoint** then discovers the account's OpenAI v1 endpoint and
offers supported, successfully provisioned synchronous chat deployments in a
dropdown. The selection auto-saves; **Deployments neu laden** refreshes the
catalog explicitly. Discovery is otherwise cached for five minutes and refreshed
on the next settings/chat access. Incompatible modalities and batch deployments
are not offered. Reasoning models are identified from catalog model metadata,
not the user-chosen deployment alias; unsupported sampling parameters are omitted.

Authentication uses an explicit client-secret credential with SDK token caching
and refresh, separate ARM and inference scopes, and no implicit local/managed
identity fallback. Only Azure public cloud is supported. Secrets, tokens and
catalogs are never saved in `data.json`. Any non-empty Foundry variable enables
identity mode: incomplete configuration, discovery or authentication errors do
**not** fall back to an API key or an unverified endpoint. Changing environment
configuration requires a restart. Private mode blocks discovery, refresh and chat.

### Manual API-key mode

With all four Foundry variables unset, configure a manual Azure
OpenAI-compatible endpoint under **Settings → AI endpoint**:

| Field        | Example                                   |
|--------------|-------------------------------------------|
| Endpoint URL | `https://my-resource.openai.azure.com` or `https://my-resource.services.ai.azure.com/openai/v1` |
| Deployment   | `model-router`                            |
| API version  | `2024-10-21` (classic API only; omitted for v1) |

The **API key** is **not** stored in `data.json`; it is supplied through the
`FORECAST_AI_API_KEY` environment variable — e.g. as a Docker secret or an
`environment` entry in `docker-compose.yml`. Locally this is enough:

```bash
FORECAST_AI_API_KEY=your-key go run ./cmd/server
```

For Docker Compose create a `.env` next to `docker-compose.yml` (template:
`.env.example`, git-ignored):

```bash
cp .env.example .env
# edit .env and set FORECAST_AI_API_KEY=...
docker compose up -d
```

For v1, the deployment is sent as `model` in the JSON body rather than embedded
in the URL. The existing classic endpoint format remains supported. Answers are
displayed as text only; the analyst never writes or expands forecast entries.

## HTTP API
A JSON API is available under `/api/v1` so external tools (e.g. a desktop
client) can **read** and **synchronize** the forecast (one hours value per
day/project), manage projects and maintain settings. The web UI intentionally
stays unauthenticated; **only `/api/**` is protected**, by two bearer tokens:

| Token environment variable  | Access                     |
|-----------------------------|----------------------------|
| `FORECAST_API_READ_TOKEN`   | read only (GET)            |
| `FORECAST_API_WRITE_TOKEN`  | read **and** write         |

If **neither** token is set the API is disabled (`503`). Like the AI key, the
tokens are **never** stored in `data.json` and are only supplied through
environment variables. The **Settings** page shows whether the two variables are
set.

```bash
# Read the current state
curl -H "Authorization: Bearer $READ" https://host/api/v1/data

# Synchronize hours (upsert per day/project; hours=0 deletes)
curl -X POST https://host/api/v1/entries/sync \
  -H "Authorization: Bearer $WRITE" -H "Content-Type: application/json" \
  -d '{"entries":[{"date":"2026-07-01","projectId":"<id>","hours":6}]}'
```

The full reference of all endpoints, parameters and examples lives in
[docs/API.md](docs/API.md).

## Logging
The application writes logs **simultaneously** to the container output
(`docker logs`) and to a file at `appdata/forecast.log`. The file rotates
automatically at **10 MB** (keeping up to three backups, `forecast.log.1`–
`forecast.log.3`); no external package is needed. AI calls are logged with
endpoint/deployment, prompt and response size, `finish_reason`, token usage and
duration (the **API key is never logged**), which makes debugging the remote
endpoint straightforward. Errors and warnings also appear in the container
output.

## Build and run with Docker
```bash
docker build -t forecast-tool .
docker volume create forecast-data
docker run -p 8080:8080 -v forecast-data:/appdata forecast-tool
```

## Run with docker compose (target host)
`docker-compose.yml` pulls the prebuilt image from the GitHub Container Registry
by default and persists the data in a named volume:
```bash
docker compose up -d
```
To build locally instead of pulling, comment out the `image:` line in
`docker-compose.yml` and enable `build: .`.

## CI/CD: checks and container release
[.github/workflows/ci.yml](.github/workflows/ci.yml) checks formatting,
`go vet`, golangci-lint, govulncheck, gosec (SARIF), race-enabled tests and the
static build.

[.github/workflows/release.yml](.github/workflows/release.yml) first re-runs
`go vet` and the race tests — so a red build can never publish an image — then
builds and pushes multi-arch images to GHCR, produces SBOM and provenance,
signs keyless with cosign and uploads the Trivy SARIF report. For a `v*` tag it
also publishes the GitHub release entry, using the annotated tag message plus
the auto-generated changelog, so tagging is the only manual release step.

[.github/workflows/codeql.yml](.github/workflows/codeql.yml) runs CodeQL
analysis on every push and pull request, plus weekly.

[.github/workflows/pages.yml](.github/workflows/pages.yml) rebuilds the
documentation site (docs, screenshots, demo snapshot) on every push to `main`
and publishes it to GitHub Pages.

Every push to `main` builds and pushes a multi-arch image tagged `latest` plus
`sha-<short>` for pinning an exact commit
(`ghcr.io/daknoblo/forecast-tool:latest`). Pushing a git tag `v1.2.3`
additionally publishes `1.2.3` and `1.2` and creates the matching GitHub
release. There are no further stages (no `stable`/`dev`).

To cut a release, annotate and push a tag — everything else is automatic:
```bash
git tag -a v1.2.3 -m "v1.2.3" -m "What changed ..."
git push origin v1.2.3
```

### One-time setup
1. **Repository permissions**: under *Settings → Actions → General → Workflow
   permissions* enable "Read and write permissions" (or rely on the
   `permissions:` block in the workflow). **No** PAT is required — the workflow
   uses the automatic `GITHUB_TOKEN`.
2. **The first push** creates the `forecast-tool` package (initially **private**).
   Under *GitHub → your profile → Packages → forecast-tool → Package settings*
   you can either:
   - make it **public** (no login needed to pull), or
   - keep it **private**.

### Pulling on the target host
- **Public image**: simply `docker compose up -d` — no login.
- **Private image**: log in once with a personal access token (classic) that has
  the `read:packages` scope:
  ```bash
  echo "<YOUR_PAT>" | docker login ghcr.io -u <your-github-user> --password-stdin
  docker compose up -d
  ```

> Note: the owner part of the image name must be lowercase, e.g.
> `ghcr.io/daknoblo/forecast-tool`. Adjust the `image:` line in
> `docker-compose.yml` to your GitHub user or organisation.

### Troubleshooting: `open /appdata/data.json: permission denied`
The image runs as a **non-root user (UID 65532)** and must write to `/appdata`.
The error appears when the mounted data directory is owned by someone else
(usually `root`, e.g. left over from an older image version that ran as root).

**Named volume** (the default in this repo): a freshly created volume gets the
right ownership automatically; the bundled `docker-compose.yml` additionally
enforces it on every start through a small `init-permissions` container:
```bash
docker compose up -d
```
One-off manual fix for an existing named volume:
```bash
docker compose down
VOL=$(docker volume ls -q | grep forecast-data | head -1)
docker run --rm -v "$VOL":/appdata alpine chown -R 65532:65532 /appdata
docker compose up -d
```

**Bind mount** (e.g. `./appdata:/appdata`, common with Dockge/Portainer): Docker
does **not** initialise the ownership here — the host directory must belong to
the container user (UID 65532). Either fix it once on the host inside the stack
directory:
```bash
sudo chown -R 65532:65532 ./appdata
```
… or — so it happens on every deploy — add the same `init-permissions` service
(with the **same** bind mount) to your compose file and make `forecast` wait for
it via `depends_on`:
```yaml
  init-permissions:
    image: alpine:3
    command: ["chown", "-R", "65532:65532", "/appdata"]
    volumes:
      - ./appdata:/appdata
    restart: "no"
  forecast:
    # ... existing configuration ...
    depends_on:
      init-permissions:
        condition: service_completed_successfully
```

## Tests
```bash
go test ./...
```

## Project structure & plan
See [docs/PLAN.md](docs/PLAN.md) for the architecture and design decisions, and
[docs/DOCSITE.md](docs/DOCSITE.md) for how the demo, the screenshots and the
published site are generated.


## License

Released under the [MIT License](LICENSE).
