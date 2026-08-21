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
  optional/`omitempty`) bound the **planned booking window**; empty = the whole
  fiscal year. `Project.Bookable(iso)` checks membership with a lexicographic
  string comparison and is used for **warnings and UI hints only** – it never
  blocks a write. `Validate` checks the date format and `startDate <= endDate`.
  `active` marks a project as **finished** rather than merely hiding it: an
  inactive project drops out of the forecast grid **and** releases the budget it
  never planned (see "Project booking window & burn rate").
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
  60) and four freely editable labels (`minLabel` "Soll Burnrate unterschritten",
  `optimalLabel` "Optimal", `highLabel` "Zu hoch", `overLabel` "Überbucht").
  `models.LegacyMinLabel` holds the previous default; `storage.normalize`
  replaces it so existing documents pick up the new wording.
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
- `FiscalYearSettings` (per FY, in `Data.FiscalYears map[int]...`): weekdayHours,
  vacationDays, holidayDays, standardTaskLabel, standardTaskHours. **There is no
  stored FY target** — it is the net of this breakdown (see below).
  `weekdayHours` overrides the gross FY hours; `0` means "follow the calendar"
  (`weekdays × 8 h`). `holidayDays` overrides the public holidays of the
  configured federal state; it is a **pointer**, because `0` is a meaningful
  override there.
- Legacy fields (`annualVacationDays`, `vacationDaysH1`/`vacationDaysH2`) are
  kept `omitempty` for migration and the `FYFor` fallback only.
  `FiscalYearSettings.MigrateVacationDays` folds the two half-year values into
  `vacationDays`; `storage.normalize` and `Data.FYFor` both call it, so no caller
  ever sees the legacy split.

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

- The hour configuration (gross hours, vacation, holidays, standard tasks) is
  stored **per fiscal year** (the values change from year to year). Global values
  (start month, federal state, weekly target) apply to all fiscal years.
- The settings page has **no year picker of its own**: it always writes to the
  fiscal year selected in the header dropdown (`POST /fy`).

## Per-FY hours configuration

- The settings page **always edits the fiscal year selected in the header**.
  There is deliberately **no second year picker and no `?year=` parameter** — the
  header dropdown (`POST /fy`) is the single place to switch the FY, for every
  tab.
- The hour block reads **top-down as one calculation**: gross FY hours →
  − vacation → − public holidays → − standard tasks → "Verbleibende zu leistende
  FY-Stunden". Keep that order; it is what makes the numbers legible.
- **That net result IS the fiscal-year target.** It is never stored and never
  entered by hand: `BuildGoalSummary`, `BuildWeekToDate` and `BuildGoalFlow` all
  take their target from `BuildFYCapacity(...).RemainingHours` (clamped at 0),
  which is why those three take a `*holidays.Calendar`. Do not reintroduce a
  `targetHours` field — two sources for the same number is exactly the bug this
  removed.
- **Gross FY hours** are an editable input pre-filled with `weekdays × 8 h`
  (`FYCapacity.WeekdayHoursAuto`); **public holidays** are an editable day count
  pre-filled from the federal state (`FYCapacity.HolidayDaysAuto`). A manually
  entered value is persisted; typing the calendar value again **clears** the
  override, so the field starts tracking the calendar once more. Never store the
  computed value as an override — it would silently go stale when the FY start
  month, year or federal state changes.
- **Vacation and public holidays are day/hour pairs** (`.field-pair`, two inputs
  side by side): the *days* are editable, the hours next to them are `readonly`,
  carry no `name` and are therefore never posted.
- Everything that changes a derived field carries `data-reload`, so the
  autosave in `partials.html` reloads the page and the server recomputes the
  read-only fields. Do **not** duplicate the arithmetic in JavaScript.
- `forecast.BuildFYCapacity(d, cal, year)` is the single source of that
  breakdown and works for **any** fiscal year (unlike `BuildGoalSummary`, which
  is bound to the active one).
- Layout: the settings form is `.form-stack.settings-form` (`max-width: none`)
  so the three **global** fields (FY start month, federal state, weekly target)
  fit on one `.form-row.globals` line, while the hour block stays in a narrow
  `.hours-block` column — it has to read as a sequence of steps.

## Vacation as a project

- Each fiscal year has exactly **one vacation project**
  (`Project.System == "vacation"`, `models.VacationSystem`), with the stable id
  `vacation-<year>` (`models.VacationProjectID`), the name "Urlaub" and the
  default colour `models.VacationColor` (#64748b). It is created/synchronized by
  `models.EnsureVacationProject(d, year)` – called in `storage.normalize`
  (load + JSON editor + first start via `load()`), on an FY switch
  (`handleSetActiveFY`) and when saving the settings (`handleSettingsSave`).
- **Budget = VacationDays × 8 h** from the FY settings
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
  vacation is an **ordinary band** like any other project. The flat vacation
  deduction in the FY capacity (goal page) stays. The projects page additionally
  shows the badge "automatisch · Urlaub".
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
- **The forecast grid's out-of-year days are shown but never summed.** A
  Monday-based FY week can start before the FY (e.g. FY 2027 begins on a
  Wednesday, so week 1 runs from 29.06.) or end after it. Those cells stay
  visible and writable — the hours belong to the neighbouring FY and reduce
  *its* budget — but `buildWeek` keeps them out of `Total`, `ProjectTotals`,
  `HolidayHours` and therefore out of the utilization, matching
  `BuildYearSummary.WeekTotals` (`FYWeekIndexOf` returns 0 outside the FY),
  `BuildSankey` and `BuildGoalSummary`. The cell keeps its own `Total`, the day
  column is marked `.outyear` with an "anderes FY" tag, and the live JS
  recalculation skips inputs carrying `data-outyear` — otherwise the numbers
  would jump on the next reload.
  `BuildYearSummary` pools the hours per assignment and fiscal year
  (`groupKey` = assignment ID, or the project ID when there is none, e.g. the
  vacation project). Derived fields: `Consumed` (hours dated **inside** the FY),
  `CarryOver` (earlier FYs), `FutureFY` (later FYs), `FYSplit []FYHours` +
  `SpansFY` (the full per-year split), `Released` (unplanned budget of an
  inactive project, see below), `AvailableBudget` (= `BudgetHours - CarryOver -
  Released`, floored at 0), `Remaining` (= `AvailableBudget - Consumed`),
  `UtilizationPct` (= `(CarryOver + Consumed) / BudgetHours`, the whole
  assignment across all fiscal years) and `CarryOverPct` (= `CarryOver /
  BudgetHours`).
- **An assignment ID may appear at most ONCE per fiscal year.** Because the
  hours are pooled per assignment, a second row of the same FY would report the
  same figures again. `models.Validate` rejects it, `handleProjectCreate` and
  `handleProjectUpdate` refuse the write, and the FY roll-ups
  (`TotalHours`/`TotalBudget`/…) count each assignment once even when an older
  document still carries a duplicate. The same assignment in **different**
  fiscal years is the normal continuation and stays allowed.
- **Entries whose project no longer exists are ignored everywhere** —
  `BuildGoalSummary`, `BuildYearSummary` (incl. its weekly totals),
  `BuildGoalFlow` and `BuildWeekToDate` all skip them, exactly like the grids,
  which iterate `d.Projects`. Otherwise the goal page and the dashboard would
  report different totals. The write paths must not create such entries either:
  `models.Validate` rejects them, so a single orphan would make every later
  `store.Mutate` fail.
- **An inactive project releases its unplanned budget.** Setting `active` to
  false means the project is done: every booked and forecast hour stays exactly
  as it is, but the part of the budget that was never planned is assumed to never
  be called off again. `BuildYearSummary` therefore caps `AvailableBudget` at
  `Consumed` and reports the difference as `Released`, which makes `Remaining`
  zero and shrinks the burn rate accordingly. Nothing is released while more was
  booked than budgeted. The flag has its **own route and its own button**
  (`POST /projects/{id}/active` with `active=0|1`); `handleProjectUpdate`
  deliberately does **not** touch it, so an autosave of the edit form can never
  flip it by accident.
- **The two shares of the current fiscal year run against `AvailableBudget`, not
  against `BudgetHours`:** `PlannedPct` = `Consumed / AvailableBudget` (booked +
  forecast, so **100 % means the available hours are completely planned**) and
  `ActualPct` = `Actual / AvailableBudget`. Measuring them against the full
  assignment budget would make a continued assignment look idle although its
  remaining budget is long gone. With `AvailableBudget == 0` an exhausted budget
  reads as 100 % instead of dividing by zero.
  `YearSummary` carries `TotalCarryOver`, `TotalReleased`, `TotalAvailable`,
  `HasCarryOver`, `HasReleased` and `HasFYSplit`.
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
  year and are already deducted as a carry-over. `BuildBurndown` pools the hours
  of **every project row of the same assignment**, so a curve started on the
  previous year's row still ends on the `Remaining` the table shows.
- The **default active fiscal year** on first start (and for a legacy document
  with `year: 0`) is the FY that contains today, via
  `forecast.FiscalYearOf(time.Now().UTC(), startMonth)` in `storage` — not the
  calendar year, which differs for half of the year with a July start.
  `models.DefaultFiscalYearStartMonth` (7) is the shared default.
- **`BuildYearSummary` takes `cal *holidays.Calendar`** (for holiday-accurate
  working days). Callers: `handleDashboard`/`handleProjects`/`handleGoal` (all
  have `s.calendar(d)`); tests pass `holidays.New(2026, "BY")`.
- **The booking window never blocks a write.** Hours dated outside it must stay
  **visible and editable everywhere** – the window is a planning hint, not a
  barrier. In the forecast grid (`week.html`) such day cells keep their input and
  are only marked (`td.day.closed` hatching + `input.hcell.outside` dashed
  border + a title saying booking is still possible), driven by the template
  function `bookable $p $d.Date`. `handleWeekSave`, `handleWeekCells` and
  `POST /api/v1/entries/sync` accept them without a `p.Bookable` guard, so such
  entries can be created, edited and deleted like any other. The only trace left
  is `ProjectSummary.OutOfWindow` (a warning) and the burn-down, which plots the
  window itself.
- The projects page shows window, working days, burn rate (h/week · h/day),
  remaining pace and, when applicable, the "outside the window" warning; the
  dashboard has the columns "Zeitraum" and "Burnrate".
- Every project's edit form carries a **button** "Auf inaktiv setzen" /
  "Wieder aktivieren" (`formaction="/projects/{id}/active"`, `formnovalidate`,
  `name="active"` with value `0`/`1`) instead of a checkbox, plus a hint
  explaining that the hours stay and only the unplanned rest is released. An
  inactive project shows the badge "inaktiv" and, once something was released,
  the KPI "Freigegeben".

## UI requirements

- **Central app name:** the constant `web.AppName` ("Forecast Tool") is rendered
  through the template function `{{appName}}` – in `<title>`, the header brand
  and the footer. Change the name in that one place only.
- **Static assets** are referenced with `{{asset "/static/style.css"}}`, which
  appends a content hash; the static handler answers with a long-lived
  `Cache-Control: immutable`. Never link a static path without `asset`.
- **Build version:** `web.Version` (template function `{{version}}`, rendered in
  the footer as `.ver`) defaults to `"dev"`. The container build stamps the real
  value through `-ldflags "-X …/internal/web.Version=$VERSION"`; the release
  workflow passes the git tag for a tag push and `<latest tag>+main` otherwise
  (hence `fetch-depth: 0` on that checkout — `git describe` needs the tags).
  Show the **release tag**, never a bare commit hash: the footer is what the
  user reads to tell which version is deployed. Never read it from a file or an
  env var — it has to describe the binary.
- **Navigation (header)** in this order and wording:
  Dashboard (`/`) – Projekte (`/projects`) – Forecast (`/week`) – Ziele
  (`/goal`) – Einstellungen (`/settings`). The active-class keys remain
  technically `dashboard`/`projects`/`week`/`goal`/`settings` (display and order
  only).
- **Footer:** `{{appName}} · Fiskaljahr {{Year}} · {{version}}` on the left, on
  the right a link to the **project repository**
  `https://github.com/daknoblo/forecast-tool`
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
- **KPI tiles (`.cards.kpi-row`, seven columns, always evenly spread across the
  width):** Week-to-date · Ø/Werktag · 6 Monate · Budget gesamt · Forecast gesamt ·
  Offen bis Ziel · Assignments · Aktuelle FY-Woche. The count tile is called
  **Assignments**, not "Projekte": several assignments can belong to the same
  customer project.
  **Every tile shows only its value and label**; the details live in a
  multi-line `title` tooltip on the card (`&#10;` for the line breaks).
- **Tile figures are never coloured.** `.kpi-value` always keeps the normal text
  colour — no red for a negative value, no orange for a rate below plan, and the
  week link inherits it too (only its hover underline marks it as clickable).
  The single exception is `.kpi-value.muted` for the `–` placeholder, which is
  not a figure. Do not reintroduce status colours here: the surrounding cards
  carry the same information already.
- **The `YearSummary.Total*` roll-ups describe the ASSIGNMENT work of the fiscal
  year and exclude the vacation project** (its budget is derived from the
  vacation days and its hours never count towards the goal, so it would inflate
  the budget and make `TotalRemaining` meaningless). `Projects` still contains
  the vacation row.
  - **Budget gesamt** shows `TotalAvailable` = `TotalBudget - TotalCarryOver -
    TotalReleased`, i.e. what is really left for this fiscal year after the hours
    an assignment already spent in earlier years and the budget inactive projects
    gave up. Its tooltip carries the budget-scoped leftover (`TotalRemaining` =
    `TotalAvailable - TotalHours`).
  - **Offen bis Ziel** measures against the **fiscal year's hour goal**, not
    against the summed assignment budgets: `handleDashboard` also calls
    `forecast.BuildGoalSummary` and the tile renders `Goal.Remaining` =
    `TargetHours - ActualTotal - ForecastRemaining` (identical to the
    "Ziele" page, vacation excluded). When the fiscal year has no goal
    (`Goal.HasTarget` false) the tile shows a `–` placeholder pointing at the
    settings.
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
  in `BuildGoalSummary`.
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
  remaining · window (date + "(…)" from `ProjectSummary.RemainingLabel`, which
  already carries its own "noch " prefix, e.g. "noch 2 Wochen und 3 Tage" /
  "noch 3 Monate", or reads "abgelaufen") · burn rate (`.burncol`: `BurnPerWeek`
  plus, when working days are left, the muted line "offen `RequiredPerWeek`" —
  **both in h/Woche** so they are comparable) · utilization (`.utilcol`): the
  **Übertrag** bar (only when `CarryOver > 0`, `CarryOverPct` against the whole
  assignment budget), the **Verplant** bar (`PlannedPct`, transparent) and the
  **Gebucht** bar (`ActualPct`, opaque), the latter two against the fiscal year's
  `AvailableBudget`. Every bar row carries a `title` naming its basis, because
  the three do not share one. No "Verbraucht" column.
- **Weekly utilization table (dashboard, `table.grid.compact.weekly`), columns in
  this order:** Woche (link `W1 · KW27` plus the grey `.weekrange` Mon–Fri range
  from `WeekTotal.RangeLabel`, e.g. "Mo. 29.06.2026 – Fr. 03.07.2026", on **one**
  line) · "Soll Stunden" · "Gebuchte Stunden" · Status (directly next to the
  hours) · Auslastung (`.weekutil`, 28rem wide, bar + percentage in a `.barrow`
  flex row so the bar fills the column).
- **The weekly tables (dashboard and goal page) list every week from 1 up to
  `YearSummary.LastPlannedWeek`** — the highest fiscal-year week that carries
  hours. Weeks without hours are shown as well, so gaps in the plan are visible;
  weeks beyond the last forecast are dropped, and the fiscal year is the hard
  upper bound (`WeekTotals` never leaves it). Do not filter the rows by
  `Hours > 0` again.
- **Shifting the horizon:** `GET /?sankey=<key>&soff=<n>` shifts the horizon by
  whole spans (negative = into the past); `forecast.shiftSankeySpan` clamps flush
  against the FY borders (`SankeyMaxOffset` bounds the parameter).
  `SankeyData.CanPrev/CanNext` drive the `.disabled` buttons. The middle
  **`Heute`** button links to `/?sankey=<key>` (no `soff`) and jumps back to the
  span around the current week; it is `.disabled` while `Offset == 0`, and
  `Offset != 0` additionally shows the `.sankey-shift` notice. Changing the
  horizon resets the offset (chips carry no `soff`).
- **Buckets are always ISO weeks**, no matter which horizon is selected – a whole
  fiscal year is ~52 week columns. The weekly rate is what drives the forecast
  accuracy, so a monthly (or multi-week) column would hide exactly the figure the
  chart exists for. Only days **inside the FY** count.
- **Dense axes are thinned, never shrunk:** `sankeyGeom.labelStep(textW)` derives
  from the column spacing how many columns to skip, and `web.bucketLabelStep`
  picks one common step from the widest week label and the widest value label, so
  the axis labels and the per-column totals always sit on the *same* columns.
  `web.axisLabels` takes that step as an argument and both dashboard charts pass
  their own, so the Sankey and the free-capacity chart stay aligned. Font sizes
  stay put – shrinking the text instead of thinning it makes the chart
  unreadable.
- Projects are coloured stacked bands (height ∝ planned hours, ribbons between
  adjacent weeks, stack order by total hours). Vertical separators delimit the
  weeks, and every annotated column is labelled with the **summed planned project
  hours**. The **legend lives inside the diagram** (top left, max. 2 rows, then
  "+N weitere"), no longer as HTML below it.
- **Vacation is a normal band in the Sankey** (grey, `BuildSankey` no longer
  filters it out) and additionally acts as the **hub for paused projects**: a
  project with hours in one bucket but none in the neighbouring vacation bucket
  gets a ribbon into (or out of) the vacation band instead of just stopping, so a
  vacation week visibly absorbs the other projects and releases them again
  afterwards. `web.pausedProjects` collects those projects and `web.splitBand`
  divides the vacation band proportionally (bottom-up, matching the stack order).
  There are no grey vacation blocks in the axis zone any more.
- **Sankey tooltips:** every band segment (`rect.node`) and every connecting
  ribbon (`path.ribbon`) carries an SVG `<title>` with the project name and the
  hours (`Alpha⏎KW31: 24 h → KW32: 25 h`) – the JavaScript-free way to a hover
  tooltip. CSS highlights the hovered band
  (`.sankey .ribbon:hover { fill-opacity: 0.65 }`, `.sankey .node:hover` gets an
  outline). The hours go through `chartHours`.
- **Bar width scales with the bucket count** (`web.nodeWidth(n, plotW)`, stored
  as `sankeyGeom.nodeW`): `plotW/n * 0.32` clamped to 12–96 px, and for a single
  bucket (1-week view, where there are no ribbons at all) `plotW * 0.45`. A fixed
  narrow bar would leave nearly the whole chart empty in the short ranges. The
  free-capacity chart uses **the same `g.nodeW`** for its columns, so both charts
  line up on the axis they share.
- **Free-capacity chart:** below the Sankey – on **the same time axis** (shared
  geometry `web.sankeyGeom`) – sits the column chart `web.freeTimeSVG` under the
  heading "Freie Kapazität": per bucket `FreeHours = CapacityHours − Total` with
  `CapacityHours = weekdays × 8h − holidays`. Because vacation is part of `Total`
  now, the resulting free time is unchanged. Columns above the zero line = free
  time (**green `#16a34a`**), below = overbooked (**red `#dc2626`**); the value
  labels carry the same semantics (`#166534` / `#b91c1c`).
- `BuildSankey(d, cal, rangeKey, offset)` therefore needs the holiday calendar.

## Private mode (presentation mode)

- The toggle sits **top right in the header, directly before the FY dropdown**
  (`.privbtn`, eye icon + "Privat"). The route `POST /private`
  (`handlePrivateToggle`) flips the cookie `forecast_private` (HttpOnly,
  SameSite=Lax, `Secure` when the request arrives over HTTPS) and returns via
  `refererPath(r)` to the originating page. It is a **per-browser display
  preference** and therefore deliberately **not** part of `data.json`.
- The mode **replaces the data, it does not mask it**: `Server.viewData(r)`
  returns `sample.Data(time.Now(), s.store.Snapshot())` instead of the real
  document, so every bar, chart, tooltip and indicator stays alive and the page
  still reads like the real thing. Masking (`•••`, zero-width bars, normalized
  Sankey columns) was removed – there is only **one** template set again and the
  chart helpers take no `private` flag.
- `handleDashboard`/`handleProjects`/`handleWeek`/`handleGoal` read through
  `viewData`; **every write keeps going through `store.Update`/`Mutate` on the
  real snapshot**, so a sample project can never be persisted. `handleSettings`
  deliberately stays on the real document: it holds no project data and its form
  has to write back exactly what it shows.
- `internal/sample` builds that document: settings and per-FY configuration are
  inherited from the real one (same fiscal year, federal state, capacity,
  traffic-light thresholds), only projects and entries are invented. It is
  deterministic per day, so reloading never reshuffles the demo. See the package
  doc for the shape (7 projects, 5 active / 2 inactive, 4 with a carry-over from
  the previous FY, the current half-year planned week by week, budgets derived
  from the planned hours so some projects stay over and others under).
- A yellow banner (`.privnote`, rendered by the `header` partial) states that
  everything on screen is sample data – without it the demo is indistinguishable
  from real figures.
- Locked while the mode is on: the **export** (link hidden *and* `GET /export`
  answers 403), the **fiscal-year switch** (`POST /fy` is a no-op, the select is
  `disabled`), the **data chat** (it would send the *real* figures), the
  **project forms** (create/edit/delete) and the **forecast grid** (cells
  `readonly`, no "clear" buttons; the live-total and auto-save JavaScript bails
  out early on `table[data-private]`).
- The JSON API (`/api/v1`) is deliberately **not** affected (machine interface).

## Working time per Werktag (§3 ArbZG)

- `forecast.BuildWorkload(d, months)` measures the **average working time per
  Werktag** over a rolling window that ends **yesterday** (today is still running
  and would only dilute the average); `BuildWorkloadPlan` is the same window
  running **forward from today** over the forecast, so the plan can be checked
  before the time is worked. The window is anchored on **today**, not on
  the reviewed fiscal year, so it may span several fiscal years — it therefore
  reads **all** entries and builds **its own holiday calendar** anchored on
  `now.Year()`; the page's `cal` (built for the FY) does not reach far enough back
  for a 12-month look-back.
- **Werktage are Monday to Saturday** minus public holidays — that is what makes
  a regular 40 h week land at ~6.7 h/Werktag, comfortably below the limit. Do not
  "fix" this to Mon–Fri: the 48 h and 60 h weekly ceilings of the law only follow
  from six Werktage.
- **Vacation is neutralised**, not counted: its hours never enter the numerator,
  and a day that carries *nothing but* vacation drops out of the Werktage. A day
  with real work stays in, so a half day is not swallowed.
- Limits: `WorkdayLimitHours = 8` (average over the balancing period of six
  months / 24 weeks) and `LongDayHours = 10` (cap for a single Werktag; `LongDays`
  counts the days **above** it — exactly 10 h is still allowed).
- Dashboard: one tile for `WorkloadTileMonths` (6). Goal page: **two** cards at
  the end, `#arbeitszeit` (Rückblick, booked) and `#arbeitszeit-plan` (Ausblick,
  forecast), each with `web.workloadSVG` over `forecast.WorkloadWindows`
  (12/6/3/1 months) plus the shared `workloadtable` partial. The chart always
  scales to at least 10 h so both reference lines stay visible; column colour
  comes from `web.workloadColor` (green / orange from 90 % / red once over).
- `Workload.Filled` ("davon belegt") counts the Werktage that carry hours.
- **The forward window stops at the planning horizon**: calendar months without a
  single planned hour are skipped entirely (`Workload.Skipped` counts them), and
  `StartLabel`/`EndLabel` describe the period actually measured, not the requested
  one. Without that, a 12-month window would average an overloaded plan back into
  the green simply because nobody has planned the second half-year yet. Several
  windows therefore collapse onto the same figure once the plan runs out — that
  is the intended signal, not a bug. The **backward** window never skips a month:
  not having worked is a fact, not a gap in the plan.

## More UI requirements

- On the goal page the **quarter and month overviews are always visible** (not
  collapsible).
- **Goal page KPI tiles (`.cards`), in this order:** Fiskaljahresziel
  (`TargetHours`) · Aktuell gebucht (`ActualTotal`) · Forecast
  (`ForecastRemaining`) · Hochrechnung (`Projected`) · Prognose %
  (`PctProjected`) · **Offen** (`Remaining` = target − booked − forecast, the
  hours still to be planned). Public holidays deliberately have **no tile** —
  they do not count towards the goal and the figure lives in the capacity table
  ("– Feiertage", hours plus days).
- **Goal page order (chronological):** whole FY (KPIs, status including the
  progress chart) → **hours flow** → FY capacity, remaining pace, target pace →
  **half-years H1 & H2** (`GoalSummary.Halves`) → **quarters**
  (`GoalSummary.Quarters`) → month overview → weekly utilization → **working time
  per Werktag** (`#arbeitszeit` backward, `#arbeitszeit-plan` forward — the only
  calendar-based sections, so they sit at the end). Half-years and
  quarters share the same card markup (`.periods > .period` with `.period-kpis`,
  a utilization bar and a `web.progressSVG` chart); `.periods.quarters` is a 2×2
  grid. The charts exist for FY, both halves and all four quarters (`handleGoal`
  builds `QuarterCharts` from `gs.Months[q*3:q*3+3]` against `TargetHours/4` and
  passes the clamped `FYMonthProgress` as the today position); **all of them must
  look the same** – they all come from `web.progressSVG`. Months and weeks keep
  their bars.
- **Hours flow (`web.goalFlowSVG` from `forecast.BuildGoalFlow`)** sits right
  below the status card: a five-stage Sankey **projects → months → quarters →
  half-years → fiscal year**. Vacation is excluded and only in-FY dates count, so
  **every stage carries the same total** – one shared scale for all columns is
  therefore mandatory, otherwise the ribbons would not match their nodes.
  Projects are ordered by their centre of gravity in the year (fewest crossings).
- **The flow is colour-coded by calendar progress**, not by period index:
  `forecast.FYMonthsDone` gives the number of completed FY months and
  `goalFlowState` maps each month/quarter/half onto **done** (`#0e7490`),
  **current** (`#1d4ed8`) or **upcoming** (`#64748b`). A ribbon keeps its
  **source** colour, so projects flow in with their own colour and everything
  from the month column onwards carries the progress colour.
- **Every stripe shows the booked share**: the node is drawn twice – the planned
  hours translucent (`fill-opacity 0.55`) and the already booked part opaque on
  top of it, growing from the bottom. Node tooltips carry
  `X h geplant · Y h gebucht` plus `Soll Z h · P % erreicht` for the periods. The
  labels inside the stripes need a **white halo**
  (`stroke="#ffffff" paint-order="stroke"`), because the translucent base offers
  no reliable contrast.
- **`web.progressSVG(labels, booked, projected, target, todayPos, wide)`**
  draws the burn-up of a period as **one continuous curve that starts at zero**:
  green `#16a34a` (booked, with a filled area) up to `todayPos`, orange `#ea580c`
  dashed (projection incl. forecast) from there to the end. Both halves meet in
  the same junction point, whose value is the cumulative **booked** total of the
  sub-period `todayPos` falls into – booked hours only exist on past days, so
  that sub-period already carries its final booked value. Right **on** a
  boundary (`todayPos` is a whole number) nothing has elapsed in the next
  sub-period yet, so the previous one is the last that counts. Do **not**
  interpolate the junction across the running sub-period: that understates the
  curve and makes an already reached target look unreached. Do
  **not** draw two independent curves and do **not** split by "completed
  sub-periods" – that hides the hours already booked in the running month.
  `todayPos` is a **fractional** sub-period index (0 = period start, `len` =
  end) from `forecast.FYMonthProgress`, clamped per period by `handleGoal`.
  Position 0 is the period start, position k the state after sub-period k, so
  the month labels sit centred **under their segment**.
  **There is no ideal-pace line.** The target is a solid red `#dc2626` line;
  the figures live in a **centred row of pills above the plot**, always in this
  order and always in the colour of what they describe: **Gebucht** (green
  `#16a34a`), **Forecast** (blue `#2563eb`, = projection − booked, never
  negative), **Hochrechnung** (orange `#ea580c`) and **Ziel** (red `#dc2626`,
  only when a target is set). Because the pills already carry those numbers,
  the `.period-kpis` line above the bar only shows Forecast (and Feiertage for
  quarters), while **Zielerreichung sits right-aligned in the `<h3>` heading
  row** (`.period-goal`) in normal text colour – never red. Y ticks come from
  `web.niceStep` (1/2/2.5/5 × 10^k) instead of halving the maximum. `wide`
  switches the viewBox from 560×232 to 1100×300 and adds `.progress-chart.wide`
  (`max-width: none`), so **only the FY chart** fills the full card width while
  the font sizes stay identical; halves and quarters stay capped at
  `max-width: 560px` so their 11 px labels remain readable.
- **The chart carries two y axes: hours on the left, share of the target on the
  right.** The right axis reuses the *same* gridlines (`v / target × 100`), so
  both sides describe one and the same curve — never give it its own scale. The
  target line is by definition the 100 % mark and is labelled as such in the
  target red; a gridline percentage closer than 13 px to it is dropped so the
  two labels cannot collide. Without a target (`target <= 0`) the axis has no
  basis and is left out entirely, and `padR` drops back from 46 to 14.
- **Chart colours on the goal page must carry real contrast.** The cards sit on
  a light background, so pale greys disappear even when dashed: gridlines are
  `#e2e8f0`, axes `#94a3b8` and every axis label `#475569`. The same applies to
  the goal flow, whose ribbons run at `fill-opacity 0.42`. Keep the swatches in
  `.flow-legend`/`.goalbar-legend` in sync with these values.
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
  Layout is a compact `.util-grid` (`auto 7rem 1fr`) inside `.form-stack.util-form`
  (`max-width: none`, otherwise the 420 px stack squeezes it): **one row per
  status** with the coloured dot, the narrow hours field and the free label field
  side by side, under a single caption row. The inputs are therefore **not**
  wrapped in `<label>` – they carry `aria-label` plus an explanatory `title`.
  "Zu hoch" has no threshold of its own and shows a muted "dazwischen" instead.
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
  range. **Vacation is excluded** — the banner measures billable work, so time
  off must not raise the rate the grid is judged against (its own row on the
  projects page still shows a burn rate). Source:
  `forecast.BuildSpanBurn(ys.Projects, spanStart, spanEnd)` in
  `handleWeek` (`ys` = `BuildYearSummary(d, cal)`).
- **Forecast control row & auto-save (`/week`):** above the table sits a
  **centred** control row (`.week-controls`): the "visible weeks" switch is
  flanked by a **«zurück»** button on the left and a **weiter»** button on the
  right (`.btn.nav-btn`, disabled at the FY borders); below it the burn-rate
  banner. There is **no save button** – changes are stored **automatically**:
  typing (debounced) or leaving/Enter on a cell sends it via `fetch` (JSON,
  `keepalive`) to **`POST /week/cells`**
  (`{cells:[{date,projectId,hours}]}`; `hours<=0` deletes; only unknown projects
  are skipped and counted; persistence via
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
  the date). The button lives in the settings page under "Konfigurationsdatei";
  in private mode the link is hidden and the route answers 403.

## Chat with your data (`POST /goal/chat`)

- **There is no JSON editor.** The `/data` page, its routes, `data.html`,
  `store.ReplaceJSON`/`ValidateJSON`/`Reset` and the `forecastPlan` expansion are
  gone; the AI is now a **read-only analyst**, it never writes data.
- The AI endpoint is configured in the **settings** (own form, `section=ai`):
  endpoint URL, deployment/model-router name, API version. The **API key** comes
  from the `FORECAST_AI_API_KEY` environment variable (Docker secret /
  `environment`), not from the UI. On save any legacy key is removed from the
  data file. Effective settings via `effectiveAI()` (env overlays the store).
- The last section of the goal page offers a **drop-down of ready-made prompts**
  (`web.chatPresets`, first entry "Fasse meine Projekte für dieses Jahr
  zusammen") plus a free text field. Selecting a preset only **fills** the input,
  so it stays editable; the server always receives plain text.
- `POST /goal/chat` takes `{"prompt": "..."}` (JSON, `DisallowUnknownFields`,
  `MaxBytesReader`, capped at `maxChatPrompt` characters) and answers
  `{"answer": "..."}` or `{"error": "..."}`. It is **disabled in private mode**
  (403) and returns 503 when no endpoint is configured.
- **The browser never ships the data.** `web.buildChatContext` renders a compact
  factual digest server-side from `BuildYearSummary`/`BuildGoalSummary`/
  `BuildGoalFlow`: totals against the goal, capacity, per-project budgets with
  booked/forecast/remaining, hours per month and quarter, and hours per project
  and month. That is a few kB instead of the whole document.
- The answer is untrusted model output and is written with **`textContent`**,
  never `innerHTML`.
- The AI client lives in `internal/ai` (stdlib only): `ai.Ask(ctx, cfg, system,
  user, logger)` posts to the Azure OpenAI-compatible URL
  `{endpoint}/openai/deployments/{deployment}/chat/completions?api-version=...`,
  auth via the `api-key` header, `temperature: 0`, a timeout, markdown-fence
  stripping and **refused redirects** (the key must not follow a redirect).
  German error messages.
- Truncated AI answers (`finish_reason: length`) are detected and reported with a
  German message; the client sets `max_completion_tokens` (8192).

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
  `hours=0` deletes; guarded by project existence only (a date outside the
  booking window is accepted); discarded entries are reported in `skipped` (the
  rest is applied). Response `{upserted, deleted, skipped}`.
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

## Documentation site, demo & screenshots

- The public site (GitHub Pages) is **generated, never hand-written**:
  `go run ./cmd/docsite -out site` starts the real application in-process on a
  loopback port against a generated demo document and derives everything from
  that instance. `.github/workflows/pages.yml` runs the same command on every
  push to `main`, so docs, demo and screenshots can never drift from the code.
- `internal/docsite` holds the four steps: `demo.go` (deterministic demo data),
  `snapshot.go` (static, clickable copy of every GET page), `shots.go` (the shot
  list handed to `tools/screenshots/capture.mjs`) and `site.go` (Markdown →
  HTML with goldmark, plus the screenshot gallery).
- **The demo data must stay deterministic**: it is anchored on today and derives
  its pattern from a hash of the date (`docsite.hash`), never from
  `math/rand`. It covers every feature that has to be visible in a screenshot –
  several assignments, the automatic vacation project, a carry-over from the
  previous fiscal year, booked days in the past and forecast days ahead.
- `internal/docsite/demo.go` and `internal/sample` are **separate on purpose**:
  the doc site needs a small, screenshot-friendly year, the private mode a full
  one that exercises every indicator. Both are deterministic; do not merge them
  or the screenshots change with every tweak to the private-mode demo.
- **The snapshot must stay inert.** `rewrite` points every internal link at its
  local file, copies the stylesheet and injects a script that swallows form
  submits and `fetch` – there is no server behind the published copy, and
  without the stub the auto-save would show a red "save failed" pill. Links to
  pages that were not captured fall back to the closest captured page
  (`fallbackURL`), so nothing dead-ends.
- **A new route belongs in `docsite.DemoPages`, a new screenshot in
  `docsite.DemoShots`, a new document in `markdownPages` + `nav`.** The shot
  title and description are German (they are the gallery caption); the rendered
  documents stay English, like the rest of the repository.
- Screenshots need `npm ci` + `npx playwright install chromium` in
  `tools/screenshots`. Without them the build only warns and skips the gallery;
  CI passes `-require-screenshots` so a broken capture fails the workflow.
- The README embeds the screenshots through their **Pages URLs**; the PNGs are
  build artifacts and are deliberately **not committed** (`/site/` is ignored).
- Enabling the site is a one-time manual step: *Settings → Pages → Source:
  GitHub Actions*. See `docs/DOCSITE.md`.

## Working conventions (for the agent)

- Before committing: `gofmt`, `go vet ./...`, `go build ./...` and
  `go test ./...` must be green.
- Then run a local smoke test (server with a temporary `FORECAST_DATA_DIR`) and
  clean up afterwards (remove `appdata`, restore `appdata/.gitkeep`).
- Do not create separate markdown documentation files unless explicitly asked.
- Templates and static files live under `internal/web/` (via `embed`), not in the
  repository root.
- **When a feature changes the UI, check `internal/docsite`**: does the demo data
  still show it, is the page part of the snapshot, does a screenshot cover it?
