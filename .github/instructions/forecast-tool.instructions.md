---
description: "Domain requirements and conventions for the forecast tool (Go web app, fiscal year, capacity/goal calculation). Use for every change to the data model, aggregation, handlers or templates."
applyTo: "internal/**,cmd/**"
---

# forecast-tool – requirements & conventions

A personal single-user forecast tool (Go web app → Docker → GHCR). This file
collects every requirement stated so far as the binding reference.

## Language policy

- **The user interface is German**: all template text, form labels and
  user-facing error/validation messages (web handlers, `models.Validate`,
  `storage`, `api`, `ai`) stay German.
- **Everything else is English**: code, identifiers, comments, doc comments,
  README, `docs/**`, workflow files, Dockerfile/compose comments and this
  instructions file.
- Chat with the user is held in German.

## Foundations / stack

- Go (standard library): `net/http` ServeMux (method + pattern routing,
  `r.PathValue`), `html/template` + `embed`. Module path
  `github.com/daknoblo/forecast-tool`.
- Storage: **a single JSON file** at `appdata/data.json` (atomic write via temp
  file + `fsync` + rename, `sync.RWMutex`). The path is overridable with
  `FORECAST_DATA_DIR` (`DATA_DIR` is a legacy alias only).
- Public holidays automatically via `github.com/rickar/cal/v2` (German federal
  states selectable); coverage year-1..year+2, because a fiscal year spans two
  calendar years. Calendars are memoized process-wide via `holidays.Get`.
- No authentication for the HTML UI (internal use only). The server listens on
  `:8080` (`FORECAST_ADDR`).
- Deployment: Dockerfile (distroless, non-root, multi-arch, healthcheck) +
  GitHub Actions → `ghcr.io/daknoblo/forecast-tool`.
- A Traefik reverse proxy sits in front, so port 8080 is internal.

## Data model

- `Project`: id, assignmentId, name, budgetHours, active, color, startDate,
  endDate. `assignmentId` (string, external assignment ID, e.g. `5641245`)
  identifies real projects uniquely and is **required** on creation (web form +
  `POST /api/v1/projects`, max. 100 characters); the vacation project has no
  `assignmentId`. `startDate`/`endDate` (ISO `YYYY-MM-DD`, inclusive, both
  optional/`omitempty`) bound the **booking window**; empty = the whole fiscal
  year. `Project.Bookable(iso)` checks membership with a lexicographic string
  comparison. `Validate` checks the date format and `startDate <= endDate`.
- `Entry`: date (YYYY-MM-DD), projectId, hours. There is exactly **one** hours
  value per day and project; whether it counts as booked (actual) or forecast
  follows from the date (past days = booked, today and later = forecast). The
  former `kind` field is a migration-only legacy field (collapsed on load in
  `storage.normalize` via `mergeEntries`: one value per (date, projectId),
  actual wins) and is never written again.
- `Settings` (global): year (= active fiscal year), federalState,
  weeklyTargetHours, fiscalYearStartMonth, `ai` (AISettings), `utilization`
  (UtilizationSettings).
- `UtilizationSettings` (global, in `Settings.Utilization`): the utilization
  traffic light. Three thresholds (`minHours` 26, `optimalHours` 40, `overHours`
  60) and four freely editable labels (`minLabel` "Burnrate Minimum",
  `optimalLabel` "Optimal", `highLabel` "Zu hoch", `overLabel` "Überbucht").
  `Settings.ClassifyUtilization(h)` maps weekly hours to a
  `UtilStatus{Key,Label,Hours}`: `h<=min`→`min` (blue, arrow down) ·
  `min<h<=optimal`→`optimal` (green, OK) · `optimal<h<over`→`high` (orange,
  arrow up) · `h>=over`→`over` (red, ✕). The basis is the week's booking (sum of
  the hours values).
- `AISettings` (in `Settings.AI`): endpoint, deployment, apiVersion. Configures a
  remote, Azure OpenAI-compatible chat-completions endpoint (e.g. an Azure AI
  Foundry model router). The **API key is NOT stored in the JSON**; it is
  supplied exclusively through the `FORECAST_AI_API_KEY` environment variable
  (`AISettings.APIKey` is `omitempty` and only a legacy read fallback).
- `FiscalYearSettings` (per FY, in `Data.FiscalYears map[int]...`): targetHours,
  vacationDaysH1, vacationDaysH2, standardTaskLabel, standardTaskHours.
- Legacy fields (`fiscalYearTargetHours`, `annualVacationDays`) are kept
  `omitempty` for migration and the `FYFor` fallback only.

## Defaults

- **Federal state: `SN` (Saxony)** – in `models.DefaultData` and
  `storage.normalize`.
- **Weekly target hours: `40`** (also as a grey `placeholder="40"` in the input).
- **Utilization traffic light** via `models.DefaultUtilization()` (applied in
  `DefaultData` and in `storage.normalize` when all three thresholds are `0` →
  legacy data): **min 26 / optimal 40 / over 60 h** with the default labels.
- Per fiscal year via `models.DefaultFYSettings()` (applied in `FYFor` when a
  fiscal year is not configured yet): **target 1440 h**, **vacation H1 15 /
  H2 15 days**, **standard tasks 250 h**. Unconfigured fiscal years are
  pre-filled with these values in the settings.

## Fiscal year (FY)

- The FY is named after the calendar year in which it **ends**. It starts on the
  1st of `FiscalYearStartMonth` in the year **before** `Year` (e.g. `Year=2027`,
  start July → **01.07.2026–30.06.2027**). Start month January = calendar year
  (no shift). The single source of truth is
  `forecast.FiscalYear(year, startMonth)`.
- **H1 = the first 6 FY months**, **H2 = the last 6 FY months**.
- Week and quarter views are FY-relative (a week index across the year boundary).
- Quarters are ordered from the FY start (e.g. Jul–Sep, Oct–Dec, Jan–Mar, Apr–Jun).
- Projects always belong to exactly one fiscal year (never across); a project
  running into another FY is created anew there, so projects must be re-created
  and stored per fiscal year. Use the **same `assignmentId`** for the new year:
  that is how the carry-over of already-booked hours is resolved (see
  "Project booking window & burn rate").
- Entries always belong to a date, which in turn falls into a fiscal year.

## Per-fiscal-year settings

- Target, vacation and standard tasks are stored **per fiscal year** (the values
  change from year to year). Global values (start month, federal state, weekly
  target) apply to all fiscal years.
- The settings page allows switching the fiscal year under review (`?year=`).
  The per-FY block is written to **that** year, but saving does **not** change
  the active fiscal year — that stays the job of the header dropdown
  (`POST /fy`).

## Vacation (per half-year)

- Vacation entitlement counts **per calendar year**, while a fiscal year spans
  two calendar years. Vacation is therefore entered **separately for H1 and H2**
  (days à 8 h).
- In the capacity view it is reported separately as "Urlaub 1. Halbjahr" /
  "Urlaub 2. Halbjahr".

## Vacation as a project

- Each fiscal year has exactly **one vacation project**
  (`Project.System == "vacation"`, `models.VacationSystem`), with the stable id
  `vacation-<year>` (`models.VacationProjectID`), the name "Urlaub" and the
  default colour `models.VacationColor` (#64748b). It is created/synchronized by
  `models.EnsureVacationProject(d, year)` – called in `storage.normalize`
  (load + JSON editor + first start via `load()`), on an FY switch
  (`handleSetActiveFY`) and when saving the settings (`handleSettingsSave`).
- **Budget = (VacationDaysH1 + VacationDaysH2) × 8 h** from the FY settings
  (`FiscalYearSettings.VacationBudgetHours`) – this is the **only** automatically
  managed value: `EnsureVacationProject` synchronizes the budget only (plus the
  name when it is empty).
- Otherwise it is a **completely normal project**: name, assignment ID, colour,
  active flag and booking window are freely editable through
  `handleProjectUpdate` and `PUT /api/v1/projects/{id}`; the budget field is
  read-only in the UI and ignored by both handlers. **Not deletable**
  (`handleProjectDelete` guard, `409` in the API; in the JSON editor `normalize`
  restores it).
- Vacation hours are **planned day by day in the forecast grid** (no automatic
  distribution). They **count towards the weekly utilization traffic light**
  (`BuildWeek`/`BuildSpan` `Total`, `BuildYearSummary` `WeekTotals`), because
  vacation consumes available working time, but **not towards the FY goal**
  (`BuildGoalSummary` skips vacation via `vacationSet`). In the dashboard Sankey
  vacation is not a band: it appears as a grey block in the axis zone and reduces
  the capacity in the free-time chart. The flat vacation deduction in the FY
  capacity (goal page) stays. The projects page additionally shows the badge
  "automatisch · Urlaub".
- The AI blueprint (`internal/ai`) contains the vacation project including its
  `system` field; the model must not delete it or change its budget.

## Standard tasks

- Hours for standard tasks across the whole FY (a single input in the settings).
  The label (`standardTaskLabel`) stays in the data model but is no longer
  maintained through the settings.
- They are **deducted from the FY total hours just like holidays and vacation**.

## Goal calculation & capacity

- Public holidays (Mon–Fri) do **not** count towards the FY goal; only recorded
  hours do (past days = booked, today and later = forecast). Holidays are purely
  informational (8 h/day, own category).
- FY total hours = all FY weekdays × 8 h (weekends excluded).
- **Available working hours (net) = weekday hours − holidays − vacation (H1+H2) −
  standard tasks.**
- Key figures: the target as a % of the FY total hours and of the available hours.
- Remaining pace until the FY end: remaining goal (target − actual), remaining
  working days, required h/day.
- The target per week/month/quarter is the FY target split evenly.

## Project booking window & burn rate

- Every project has a booking window (`startDate`/`endDate`, inclusive; empty =
  the FY). `forecast` clamps the window to the FY
  (`projectWindow(p, fyStart, fyEnd)`).
- **Assignments across fiscal years:** a project belongs to exactly one FY, so a
  continuing assignment is re-created in the new year with the **same
  `assignmentId`** and the assignment's total budget. Hours are attributed to a
  fiscal year by their **DATE**, never by the project row they were booked on:
  `forecast.FiscalYearOf(t, startMonth)` is the single rule (inverse of
  `FiscalYear`), so with a July start everything up to 30 June stays in the old
  FY and everything from 1 July counts towards the new one — even when it was
  entered on the previous year's project row (the forecast grid's first/last
  week can reach across the boundary).
  `BuildYearSummary` pools the hours per assignment and fiscal year
  (`groupKey` = assignment ID, or the project ID when there is none, e.g. the
  vacation project). Derived fields: `Consumed` (hours dated **inside** the FY),
  `CarryOver` (earlier FYs), `FutureFY` (later FYs), `FYSplit []FYHours` +
  `SpansFY` (the full per-year split), `AvailableBudget` (= `BudgetHours -
  CarryOver`, floored at 0), `Remaining` (= `AvailableBudget - Consumed`),
  `UtilizationPct` (= `(CarryOver + Consumed) / BudgetHours`) and `CarryOverPct`.
  `YearSummary` carries `TotalCarryOver`, `TotalAvailable`, `HasCarryOver` and
  `HasFYSplit`.
- **Because of that, `BuildYearSummary(d, cal)` must be called with the projects
  of ALL fiscal years**; it filters to `d.Settings.Year` itself via
  `models.ProjectsForFY`. Never narrow `d.Projects` before calling it, otherwise
  the carry-over is silently zero.
- `ProjectSummary` (from `BuildYearSummary(d, cal)`) additionally carries:
  `StartDate`/`EndDate` + `StartLabel`/`EndLabel` (DD.MM.YYYY),
  `HasCustomWindow`, `WindowWorkdays` (Mon–Fri minus holidays in the window,
  `cal`-based), `BurnPerWeek` (= available budget / (working days / 5)),
  `BurnPerWorkday` (= available budget / working days), `RemainingWorkdays`
  (from today to the window end), `RequiredPerWorkday` (= remaining budget /
  remaining working days), `RequiredPerWeek` (= `RequiredPerWorkday * 5`) and
  `OutOfWindow` (hours booked outside the window – a warning).
- **The burn rate is always based on `AvailableBudget`, never on `BudgetHours`**,
  so a continued assignment does not get its already-burned hours back. The same
  applies to the burn-down chart on the projects page, which also ignores hours
  dated outside the (FY-clamped) booking window — they belong to another fiscal
  year and are already deducted as a carry-over.
- The **default active fiscal year** on first start (and for a legacy document
  with `year: 0`) is the FY that contains today, via
  `forecast.FiscalYearOf(time.Now().UTC(), startMonth)` in `storage` — not the
  calendar year, which differs for half of the year with a July start.
  `models.DefaultFiscalYearStartMonth` (7) is the shared default.
- **`BuildYearSummary` takes `cal *holidays.Calendar`** (for holiday-accurate
  working days). Callers: `handleDashboard`/`handleProjects`/`handleGoal` (all
  have `s.calendar(d)`); tests pass `holidays.New(2026, "BY")`.
- **Booking is blocked outside the window**: in the forecast grid (`week.html`)
  day cells outside the window are rendered as `td.day.closed` (with `–`, no
  inputs) via the template function `bookable $p $d.Date`. `handleWeekSave` and
  `handleWeekCells` additionally enforce it server-side (`p.Bookable(date)`
  guard), so hand-crafted posts outside the window are discarded too.
- The projects page shows window, working days, burn rate (h/week · h/day),
  remaining pace and, when applicable, the "outside the window" warning; the
  dashboard has the columns "Zeitraum" and "Burnrate".

## UI requirements

- **Central app name:** the constant `web.AppName` ("Forecast Tool") is rendered
  through the template function `{{appName}}` – in `<title>`, the header brand
  and the footer. Change the name in that one place only.
- **Static assets** are referenced with `{{asset "/static/style.css"}}`, which
  appends a content hash; the static handler answers with a long-lived
  `Cache-Control: immutable`. Never link a static path without `asset`.
- **Navigation (header)** in this order and wording:
  Dashboard (`/`) – Projekte (`/projects`) – Forecast (`/week`) – Ziele
  (`/goal`) – JSON (`/data`) – Einstellungen (`/settings`). The active-class keys
  remain technically `dashboard`/`projects`/`week`/`goal`/`data`/`settings`
  (display and order only).
- **Footer:** `{{appName}} · Fiskaljahr {{Year}}` on the left, on the right a
  link to the **project repository** `https://github.com/daknoblo/forecast-tool`
  with an inline SVG icon (no external asset, because of `embed`). The footer
  always spans the **same width as the page's `main`**: it carries the same
  `wide` class (`<footer class="foot{{if .Wide}} wide{{end}}">`) and the CSS
  mirrors the `main`/`main.wide` widths. No weekly target in the footer.
- **Dashboard utilization Sankey:** the dashboard page is `Wide` (full width) and
  shows – after the KPI cards, before "Budgets" – a card "Auslastung" with a
  server-rendered, JavaScript-free Sankey/alluvial diagram (`web.sankeySVG` from
  `forecast.BuildSankey`). Above it **two centred rows**: `.sankey-nav` with
  `‹ zurück` / `Heute` / `weiter ›` (`.btn.nav-btn`), below it
  `.span-ctl.sankey-ctl` with
  the horizon switches (`forecast.SankeyRanges`: 1 week/2 weeks/4 weeks/2 months/
  3 months/half-year/fiscal year) as `.chip` links (`GET /?sankey=<key>`, default
  `4w`, unknown → default via `NormalizeSankeyRange`).
- **KPI tiles (`.cards.kpi-row`, six columns, always evenly spread across the
  width):** Week-to-date · Budget gesamt · Forecast gesamt · Offen bis Ziel ·
  Projekte · Aktuelle FY-Woche.
  **Every tile shows only its value and label**; the details live in a
  multi-line `title` tooltip on the card (`&#10;` for the line breaks). All
  figures go through `hours`/`pct`, so private mode masks the tooltips too.
- **The `YearSummary.Total*` roll-ups describe the ASSIGNMENT work of the fiscal
  year and exclude the vacation project** (its budget is derived from the
  vacation days and its hours never count towards the goal, so it would inflate
  the budget and make `TotalRemaining` meaningless). `Projects` still contains
  the vacation row.
  - **Budget gesamt** shows `TotalAvailable` = `TotalBudget - TotalCarryOver`,
    i.e. what is really left for this fiscal year after the hours an assignment
    already spent in earlier years.
  - **Offen bis Ziel** shows `TotalRemaining` = `TotalAvailable - TotalHours`
    (neither booked nor forecast yet). A negative value gets
    `.kpi-value.negative`, but never in private mode, where the colour would
    leak the sign of the masked figure.
- **Week-to-date tile (first):** `forecast.BuildWeekToDate(d)` reports the
  **utilization reached since the fiscal year started**, up to the current week.
  The FY goal is spread evenly over the fiscal year's weeks
  (`TargetPerWeek = TargetHours / FYWeeks`, e.g. 1440 h / 52 = 27.7 h); booking
  40 h in such a week is 12.3 h above plan, so `RatePct = PerWeek /
  TargetPerWeek * 100` lands above 100 %. Elapsed time is measured in weekdays
  since the FY start divided by 5 (`ElapsedWeeks`), so the tile moves every day.
  **Today is excluded on purpose** – it is still forecast. **Public holidays DO
  count** as elapsed: the annual goal does not shrink because of them, so they
  really do put you behind the even split. **Vacation is excluded**, exactly as
  in `BuildGoalSummary`. `< 100 %` colours the value (`.kpi-value.under`), but
  never in private mode, where the class would leak the masked figure.
  `HasData` is false when the reviewed fiscal year does not contain today, has
  no goal, or has not had a single weekday yet; the tile then shows a
  placeholder.
  The tile itself shows **only the percentage and the label** – the whole
  calculation (achieved vs. planned h/week, hours since the FY start, elapsed
  weeks) lives in a multi-line `title` tooltip on the card (`&#10;` for the line
  breaks).
- **Budgets table (`table.grid.budgets`), columns in this order:** project
  (colour dot + name + `assignmentid` badge) · budget · **Übertrag** (only
  rendered when `Summary.HasCarryOver`, shows `−CarryOver`) · forecast · booked ·
  remaining · window (date + "(noch …)" from `ProjectSummary.RemainingLabel`,
  e.g. "2 Wochen und 3 Tage" / "3 Monate") · burn rate (`.burncol`: `BurnPerWeek`
  plus, when working days are left, the muted line "offen `RequiredPerWeek`" —
  **both in h/Woche** so they are comparable) · utilization (`.utilcol`, two bars:
  forecast/budget from `ForecastPct` (transparent) and booked/budget from
  `ActualPct` (opaque), preceded by a third Übertrag bar when `CarryOver > 0`).
  No "Verbraucht" column.
- **Weekly utilization table (dashboard, `table.grid.compact.weekly`), columns in
  this order:** Woche (link `W1 · KW27` plus the grey `.weekrange` Mon–Fri range
  from `WeekTotal.RangeLabel`, e.g. "Mo. 29.06.2026 – Fr. 03.07.2026", on **one**
  line) · "Soll Stunden" · "Gebuchte Stunden" · Status (directly next to the
  hours) · Auslastung (`.weekutil`, 28rem wide, bar + percentage in a `.barrow`
  flex row so the bar fills the column).
- **Shifting the horizon:** `GET /?sankey=<key>&soff=<n>` shifts the horizon by
  whole spans (negative = into the past); `forecast.shiftSankeySpan` clamps flush
  against the FY borders (`SankeyMaxOffset` bounds the parameter).
  `SankeyData.CanPrev/CanNext` drive the `.disabled` buttons. The middle
  **`Heute`** button links to `/?sankey=<key>` (no `soff`) and jumps back to the
  span around the current week; it is `.disabled` while `Offset == 0`, and
  `Offset != 0` additionally shows the `.sankey-shift` notice. Changing the
  horizon resets the offset (chips carry no `soff`).
- Buckets are weeks (up to 2 months) or months (from 3 months, half-year, fiscal
  year); only days **inside the FY** count. Projects are coloured stacked bands
  (height ∝ planned hours, ribbons between adjacent buckets, stack order by total
  hours); the **vacation project is not a band**. Vertical separators delimit the
  weeks/months, and every column is labelled with the **summed planned project
  hours**. The **legend lives inside the diagram** (top left, max. 2 rows, then
  "+N weitere"), no longer as HTML below it. **Planned vacation** appears as a
  **grey block in the axis zone** directly above the week/month label
  (`web.vacationBlocks`).
- **Sankey tooltips:** every band segment (`rect.node`) and every connecting
  ribbon (`path.ribbon`) carries an SVG `<title>` with the project name and the
  hours (`Alpha⏎KW31: 24 h → KW32: 25 h`) – the JavaScript-free way to a hover
  tooltip. CSS highlights the hovered band
  (`.sankey .ribbon:hover { fill-opacity: 0.65 }`, `.sankey .node:hover` gets an
  outline). The hours go through `chartHours`, so private mode masks them too.
- **Bar width scales with the bucket count** (`web.nodeWidth(n, plotW)`, stored
  as `sankeyGeom.nodeW`): `plotW/n * 0.32` clamped to 12–96 px, and for a single
  bucket (1-week view, where there are no ribbons at all) `plotW * 0.45`. A fixed
  narrow bar would leave nearly the whole chart empty in the short ranges. The
  free-capacity chart uses **the same `g.nodeW`** for its columns, so both charts
  line up on the axis they share.
- **Free-capacity chart:** below the Sankey – on **the same time axis** (shared
  geometry `web.sankeyGeom`) – sits the column chart `web.freeTimeSVG` under the
  heading "Freie Kapazität": per bucket `FreeHours = CapacityHours − Total` with
  `CapacityHours = weekdays × 8h − holidays − vacation`. Columns above the zero
  line = free time (blue), below = overbooked (red).
- `BuildSankey(d, cal, rangeKey, offset)` therefore needs the holiday calendar.

## Private mode (presentation mode)

- The toggle sits **top right in the header, directly before the FY dropdown**
  (`.privbtn`, eye icon + "Privat"). The route `POST /private`
  (`handlePrivateToggle`) flips the cookie `forecast_private` (HttpOnly,
  SameSite=Lax, `Secure` when the request arrives over HTTPS) and returns via
  `refererPath(r)` to the originating page. It is a **per-browser display
  preference** and therefore deliberately **not** part of `data.json`.
- Implementation: `NewServer` prepares **two template sets** once at startup –
  `s.tpl` with `privacyFuncs(false)` and `s.tplPrivate` with
  `privacyFuncs(true)`; `web.render` picks one per request. This masks **every**
  figure in **every** template as `•••` via `hours`/`pct` and makes `barWidth`
  return `0`, without touching a single call site. Clone the base set **before**
  either is executed (a template set can no longer be cloned afterwards). Form
  fields use `hoursRaw` (never masked) so the settings stay editable.
- Project names: `maskIfPrivate(d, r)` replaces them with stable placeholders
  ("Projekt A/B/…", ordered by project ID) and clears the `assignmentId`. It is
  applied in `handleDashboard`/`handleProjects`/`handleWeek`/`handleGoal` **on
  the snapshot** – write paths keep using `store.Update`/`Mutate`, so a masked
  name can never be persisted.
- Charts must not leak anything through **heights** either: `sankeySVG`
  normalizes every column to the same height (only the project mix stays
  visible) and hides the Y axis; `freeTimeSVG` shows only the direction
  (free/overbooked) at a fixed height; `burndownSVG`/`progressSVG` mask their
  axis and target labels. `body.private .bar span { display: none }`
  additionally neutralizes all HTML bars.
- Locked while the mode is on: the **JSON editor + export** (`/data` shows only a
  hint), the **project forms** (create/edit/delete) and the **forecast grid**
  (cells `readonly`, no "clear" buttons; the live-total and auto-save JavaScript
  bails out early on `table[data-private]`).
- The JSON API (`/api/v1`) is deliberately **not** affected (machine interface).

## More UI requirements

- On the goal page the **quarter and month overviews are always visible** (not
  collapsible).
- **Goal page order (chronological):** whole FY (KPIs, status including the
  progress chart, FY capacity, remaining pace, target pace) → **half-years H1 &
  H2** (`GoalSummary.Halves`, one card each with figures, a utilization bar and a
  small progress/burn chart) → quarters → month overview → weekly utilization.
  The charts (`web.progressSVG`, cumulative projection vs. ideal line + target)
  exist for FY, H1 and H2; months and weeks keep their bars.
- **Forecast grid layout:** the project-name column (`.pname`) is wide (~240 px),
  all values are centred (except `.pname`), project rows are separated by a
  horizontal rule (`tbody td` border-bottom 2px) and the week-total column
  (`.weeksum`) is narrow. These rules live in `static/style.css` (no markup
  needed in `week.html`).
- **Central FY dropdown top right in the header** (where year/state are shown):
  switches the active fiscal year globally, works from **every** page and returns
  to the originating page (route `POST /fy`, redirect to the referer path). When
  the FY is switched, every page must show the matching fiscal year.
- The settings show the **path and size of the configuration file** (JSON), the
  size as B/KB/MB. That card sits **at the very bottom** of the settings page
  (after the AI-endpoint card).
- **AI endpoint card (settings):** element order top to bottom: input fields
  (endpoint/deployment/API version) → `API-Key` label with a status indicator
  (env set / not set) → hint about the environment variable → **save button at
  the bottom**.
- **Utilization traffic-light card (settings):** its own card "Auslastungs-Ampel
  (global)" with its own form (`section=utilization`): three thresholds
  (`utilMin`/`utilOptimal`/`utilOver`) and four label fields
  (`utilMinLabel`/`utilOptimalLabel`/`utilHighLabel`/`utilOverLabel`). It sits
  between the per-FY card and the AI-endpoint card.
- **Forecast page (`/week`):** the grid is **grouped per week** (`.Span.Blocks`):
  after the five day columns each week has a **week-total column** (`.weeksum`,
  hours per project), and at the far right a **grand-total column** across all
  visible weeks (`.grandsum`). Weeks are delimited by a thicker left border on
  the first day (`.weekstart`); **month ends** by a coloured right rule
  (`.monthend`, set through `DayCell.MonthEnd`: the next visible weekday falls
  into a new month). Past (booked) days are subtly marked (`td.day.past`, badge
  "gebucht"). The `tfoot` row "Stunden / Tag" (`.dayfoot`) is **centred**
  (`td.center`) and carries week/grand totals too. Header, day and footer rows
  all iterate over `.Span.Blocks` → `.Days` so the columns stay aligned; the
  week-total headers are `rowspan=2`.
- **Burn-rate banner (`/week`):** above the table, directly **below** the centred
  control row, `.burnbanner` shows the combined burn rate
  (`{{.Burn.PerWeek}}` h/week · `{{.Burn.PerWorkday}}` h/day) plus per-project
  chips for all **active** projects whose booking window overlaps the visible
  range. Source: `forecast.BuildSpanBurn(ys.Projects, spanStart, spanEnd)` in
  `handleWeek` (`ys` = `BuildYearSummary(d, cal)`).
- **Forecast control row & auto-save (`/week`):** above the table sits a
  **centred** control row (`.week-controls`): the "visible weeks" switch is
  flanked by a **«zurück»** button on the left and a **weiter»** button on the
  right (`.btn.nav-btn`, disabled at the FY borders); below it the burn-rate
  banner. There is **no save button** – changes are stored **automatically**:
  typing (debounced) or leaving/Enter on a cell sends it via `fetch` (JSON,
  `keepalive`) to **`POST /week/cells`**
  (`{cells:[{date,projectId,hours}]}`; `hours<=0` deletes; project existence +
  `p.Bookable` guard, discarded cells are counted; persistence via
  `store.Mutate`). The page is **never reloaded** while entering data; a status
  pill (`[data-save-status]`: "Automatisch gespeichert" / "Speichert…" /
  "Gespeichert ✓" / "Fehler beim Speichern") gives feedback. Grid rows and inputs
  are ~20 % larger; the **vacation row** is subtly tinted (`tr.vacrow`), and a
  blank row (`tr.footspacer`) visually detaches the total/utilization rows in the
  `tfoot`. The former bulk `POST /week/{week}` is kept as a fallback.
- **Clear buttons** (`.clearbtn`, `type=button`, `data-clear-dates`) in the week
  and day header rows clear all `input.hcell` with a matching `_<date>` suffix
  via JavaScript; the cleared cells are auto-saved as well (`hours 0` → delete).
  A **status row** in the `tfoot` shows the traffic-light dot plus the weekly
  hours per week (`colspan=6`: 5 days + the total column).
- **Projects page:** the KPI row shows budget, consumed, remaining, **burn rate**
  (h/week) and utilization; below it the window/burn-rate block
  (`.project-window`). When the assignment spans fiscal years (`SpansFY`) a
  `.fy-split` block lists one `.fy-chip` per fiscal year (`past` / `current` /
  `future`) with a short note that the attribution follows the booking date.
- **No save buttons – everything saves itself.** Beside the forecast grid, the
  **project edit form** and all three **settings forms** are marked
  `data-autosave` and have no submit button. The shared `{{template "autosave"}}`
  block in `partials.html` posts the whole form via `fetch` (URL-encoded, so
  `r.ParseForm` can read it) whenever a field fires `change` (blur, select,
  checkbox, date/colour picker) and reports the result in a `.save-status` pill.
  A field marked `data-reload` also changes server-rendered text on the page
  (FY start month, the three traffic-light thresholds), so the page is reloaded
  after its save. The handlers answer such a request with `204` instead of a
  redirect (`web.isAutoSave`). The **JSON editor keeps its explicit save button**
  – replacing the whole document must stay a deliberate action.
- **Project edit form (`form.edit-form`), two rows:** first row Name · Assignment
  ID · Budget; second row Start · Ende · Farbe · aktiv · **Löschen** · status
  pill. Delete is a submit button inside the same form using
  `formaction="/projects/{id}/delete"` + `formnovalidate` (no nested forms) and
  keeps its confirmation dialog. The vacation project has no delete button.
- **Traffic-light dots** are rendered through the template partial
  `{{define "utilstatus"}}` (in `partials.html`): a coloured circle (`.util-dot`)
  with a white symbol (↓ / OK / ↑ / ✕) plus label. They appear in the forecast
  status row and in the "Status" column of the weekly tables on the dashboard
  (`.Summary.WeekTotals`) and the goal page (`.WeekTotals`, passed by
  `handleGoal` from an FY-filtered `BuildYearSummary`).

## Security conventions

- The HTML UI stays unauthenticated, but `web.Handler()` wraps it in two
  middlewares from `internal/web/middleware.go`:
  - `securityHeaders` sets `Content-Security-Policy` (same-origin;
    `'unsafe-inline'` is required because the templates use inline scripts,
    handlers and style attributes), `X-Content-Type-Options`, `X-Frame-Options`,
    `Referrer-Policy: same-origin` (the referer-based redirects depend on it) and
    `Permissions-Policy`.
  - `requireSameOrigin` rejects state-changing UI requests that a browser reports
    as cross-site (`403`) – the CSRF defence. Requests without `Sec-Fetch-Site`
    and without `Origin` (curl, scripts) stay allowed.
- `/api/v1` is mounted **outside** the same-origin guard: it authenticates with a
  bearer token, which a cross-site form post can never supply.
- Secrets (AI key, API tokens) come from environment variables only – never from
  `data.json`, never logged. `GET` responses redact `AI.APIKey`.
- The AI client uses its own `http.Client` that **refuses redirects**, so the
  `api-key` header cannot be forwarded to another host.
- Every user-supplied string is length-capped (`capLen`), dates are validated
  (`validISODate`/`optionalISO`) and colours are checked (`models.IsHexColor` /
  `web.sanitizeColor`) before they end up in `template.HTML` SVG output.
- New charts are always server-rendered inline SVG – never a JavaScript
  dependency.

## Performance conventions

- Holiday calendars are expensive to build (five calendar years day by day):
  always obtain them via `holidays.Get(year, state)` (memoized), never
  `holidays.New` in a request path.
- The two template sets are cloned **once at startup**, never per request.
- Aggregation helpers take a pre-built hours index where a caller loops
  (`buildWeek` inside `BuildSpan`); avoid rebuilding `hoursIndex` per iteration
  and avoid `time.Parse` inside nested loops.
- `web.render` renders into a buffer first so a template failure cannot emit a
  half-written page.

## Export

- The current JSON must be exportable/downloadable from within the application.
- Download route `GET /export` (Content-Disposition attachment, file name with
  the date). The export button lives in the JSON editor (`/data`), not in the
  settings.

## JSON editor (`/data`)

- Its own "JSON" page in the navigation: a large, wide text area (full card
  width) to edit the **whole** data file in the browser – e.g. to paste
  AI-generated JSON.
- **Validation before saving** (`store.ReplaceJSON` → `models.Validate`): strict
  parsing (`DisallowUnknownFields`, no trailing data), referential checks (every
  `entries.projectId` must exist). Invalid input is rejected with a German error
  message, **the input is preserved** and the store is never overwritten. A
  successful save shows the canonical (normalized) form.
- `POST /data/reset` clears all projects and bookings but keeps every setting;
  the browser asks for confirmation first.
- Persistence stays atomic (temp file + fsync + rename).

## AI update of the JSON

- The AI endpoint is configured in the **settings** (own form, `section=ai`):
  endpoint URL, deployment/model-router name, API version. The **API key** comes
  from the `FORECAST_AI_API_KEY` environment variable (Docker secret /
  `environment`), not from the UI. On save any legacy key is removed from the
  data file. Effective settings via `effectiveAI()` (env overlays the store).
- The JSON editor has a **prompt field**; `POST /data/ai` sends the prompt **and
  the current (possibly edited) editor content** to the endpoint and writes the
  result back into the text area. Without a configured endpoint a hint is shown
  instead.
- The AI client lives in `internal/ai` (stdlib only): Azure OpenAI-compatible URL
  `{endpoint}/openai/deployments/{deployment}/chat/completions?api-version=...`,
  auth via the `api-key` header, `response_format: json_object`,
  `temperature: 0`, a timeout, and markdown-fence stripping. German error
  messages.
- The system prompt carries a **blueprint** (`ai.Blueprint`) – a complete, valid
  example document – so the remote model knows the field names, nesting and value
  types of the forecast JSON. Keep the blueprint in sync with schema changes.
- **Compact forecast directives (`forecastPlan`):** instead of writing out
  hundreds of daily entries (which blows the token limit and produces truncated
  responses), the model emits exactly **one** entry per project in `forecastPlan`
  for regular forecasts spread evenly over a whole FY: `{ projectId, fiscalYear,
  hoursPerWeek }`. `ai.ExpandPlan` expands that **server-side** and
  deterministically into Mon–Fri entries (`hoursPerWeek/5` per working day) for
  the whole FY, deduplicates against existing entries and removes `forecastPlan`
  (cap `maxExpandedEntries`). The expansion runs in `handleDataAI` **before**
  validation; `entries` is only for individually named days. `forecastPlan` is
  not part of `data.json` (transport format only).
- **The AI answer is never saved automatically**: it is only inserted and checked
  immediately via `store.ValidateJSON`. Saving happens on an explicit
  "Speichern" (which runs the full validation again).
- Truncated AI answers (`finish_reason: length`) are detected and reported with a
  German message; the client sets `max_completion_tokens` (32768).

## HTTP API (`/api/v1`)

- Its own package `internal/api` (stdlib only), mounted in `web.Handler()` via
  `mux.Handle("/api/", api.New(store, logger))`. It has its own `ServeMux` with
  **full** paths `GET /api/v1/...` (no StripPrefix), wrapped in the auth
  middleware.
- **Two bearer tokens** from environment variables (exported constants
  `api.ReadTokenEnv` = `FORECAST_API_READ_TOKEN`, `api.WriteTokenEnv` =
  `FORECAST_API_WRITE_TOKEN`). Read = `GET` only; write = read + write. The
  comparison is constant-time (`crypto/subtle`). Missing/invalid token → `401`, a
  read token on a write endpoint → `403`, **both** tokens empty → `503`
  (fail-closed). Tokens are **never** stored in `data.json` and **never** logged.
  **The HTML UI stays unauthenticated.**
- The **settings** show (read-only card "API-Zugriff", between the AI-endpoint
  and the configuration-file card) whether the two token variables are set –
  analogous to the AI key status (`APIReadSet`/`APIWriteSet` in
  `handleSettings`).
- **Write paths go through `store.Mutate`** (copy-on-write: clone → mutate →
  `normalize` → `models.Validate` → persist and swap only on success). Reads use
  `Snapshot()`/`Marshal()`. `GET` responses **redact** the AI key
  (`AI.APIKey=""`).
- **Endpoints:** read (`GET`): `/data`, `/settings`,
  `/projects[?fiscalYear=&all=]`, `/projects/summary[?fiscalYear=]` (computed
  consumed/remaining/utilization per project from
  `BuildYearSummary`→`ProjectSummary`), `/projects/{id}`,
  `/entries[?from=&to=&projectId=]`, `/goal[?year=]`. Write:
  `POST /entries/sync`, `POST /projects`, `PUT /projects/{id}`,
  `DELETE /projects/{id}`, `PUT /settings`,
  `PUT /settings/fiscal-years/{year}`.
- **`POST /entries/sync`** is the core: upsert per `(date, projectId)`,
  `hours=0` deletes; guarded by project existence + `p.Bookable(date)`; discarded
  entries are reported in `skipped` (the rest is applied). Response
  `{upserted, deleted, skipped}`.
- Vacation project: `PUT` is allowed (only `budgetHours` is ignored), `DELETE` →
  `409`. The FY-settings `PUT` synchronizes the vacation budget via
  `EnsureVacationProject`.
- Error format `{ "error": "<German>" }`. Request bodies are strict
  (`DisallowUnknownFields`), limited via `http.MaxBytesReader` (2 MiB).
  Reference documentation: `docs/API.md` (keep it in sync with schema/endpoint
  changes), environment variables in README/`.env.example`/compose.

## Logging

- The logging package `internal/logging` (stdlib only): `Setup(dataDir)` returns
  a `*slog.Logger` that writes **simultaneously to stdout (the Docker container
  output) and to a file** `appdata/forecast.log` (text handler).
- **Self-rotation**: when the file exceeds **10 MB** (`DefaultMaxBytes`) it is
  rotated (`forecast.log.1..3`, `DefaultMaxBackups`) and the oldest is dropped.
  No external package.
- `main.go` calls `logging.Setup`, sets `slog.SetDefault` and routes the standard
  `log` output through an adapter into the same logger (everything ends up in the
  container output **and** the log file).
- **AI usage is logged** (endpoint/deployment/API version, prompt and JSON size,
  status, `finish_reason`, token usage, duration, success/error/truncation) –
  **never the API key**. Errors and warnings therefore also show up in the
  container output for debugging.

## Working conventions (for the agent)

- Before committing: `gofmt`, `go vet ./...`, `go build ./...` and
  `go test ./...` must be green.
- Then run a local smoke test (server with a temporary `FORECAST_DATA_DIR`) and
  clean up afterwards (remove `appdata`, restore `appdata/.gitkeep`).
- Do not create separate markdown documentation files unless explicitly asked.
- Templates and static files live under `internal/web/` (via `embed`), not in the
  repository root.
