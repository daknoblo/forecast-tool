# Features and planning

The documentation uses English descriptions for pages and actions; on-screen
labels depend on the application language.

## Overview

- **Project budgets:** manage projects, assignment IDs, colours and booking
  windows. Closing a project preserves its entries and releases unused,
  unplanned budget.
- **Cross-year assignments:** reuse an assignment ID and its total budget in
  another fiscal year; earlier years' consumed hours are deducted as carry-over.
- **Monthly planning:** a weekday calendar with project hours, holidays,
  vacation, capacity warnings and optional AI-generated plans.
- **Dashboard and goals:** utilization indicators, budget burn-down, capacity
  charts and fiscal-year progress. Charts are rendered as SVG on the server.
- **Forecast Accuracy:** import ESXP's YTD percentage and view its worst-case
  fiscal-year-end minimum. Missing or stale observations are identified.
- **Working-time indicators:** compare booked and planned hours with a rolling
  six-month average under German working-time rules (§3 ArbZG). These indicators
  are not a legal approval of a plan.
- **Private mode:** replace displayed figures with sample data for screen
  sharing. AI chat, planning and prompt editing are blocked in this mode.
- **AI chat:** ask read-only questions about the active fiscal year using a
  configurable Azure OpenAI-compatible deployment.
- **JSON storage and API:** export data from Settings or synchronize daily
  hours through the bearer-token-protected [HTTP API](API.md).

## Dashboard target progress

The first dashboard tile has two values separated like the six-month average:

- **Week-to-date:** booked hours since the fiscal-year start compared with the
  FY target spread evenly over elapsed weeks. This measures pace, not the
  percentage of the full annual target already achieved.
- **FY target:** project hours booked before today divided by the full FY
  hours target. Forecast and vacation are excluded. This is the same actual
  target achievement shown on the Goals page and can exceed 100%.

Each half has its own explanatory tooltip. With a target but no booked hours,
FY target shows 0%; without a target it shows a dash. Its value remains
available when reviewing another fiscal year, even if Week-to-date is not.

The **Total budget** tile also uses a split layout: available assignment hours
on the left and **FY coverage** on the right. Coverage is the available budget
divided by the full FY hours target, multiplied by 100. It uses the same budget
as the hours figure, after carry-over and released budget are deducted and
excluding vacation. It measures budget coverage, not booked or planned usage.
Values above 100% are retained; no budget gives 0% when a target exists, and
no FY target gives a dash while the hours remain visible.

## Application language

Under **Settings → Language**, choose **Deutsch** or **English**. The selection
auto-saves and reloads the page. It is stored globally as `settings.language`
in `data.json`, so it survives page reloads and application restarts. German
(`de`) is the default; English uses `en`.

Language selection applies to Dashboard, Projects, Monthly planning, Goals
and Settings, including application-owned dates, statuses, charts, UI errors,
the Forecast Accuracy label and built-in AI prompts.

Only application-owned text is translated. Your project names, custom labels,
custom prompts and other user-authored content are preserved, not translated.
External API error messages intentionally retain their German contract.
External clients can also change the language through the
[settings API](API.md#put-apiv1settings--global-settings).

## Monthly planning

Open **Monthly planning** (`/month`) to review daily project hours and weekly
capacity. Past dates show stored bookings; today and future dates initially
show a **read-only local estimate** based on recent booking patterns. Viewing
this estimate neither saves data nor calls an AI service.

To create and save an AI plan:

1. Configure an [AI deployment](AI.md).
2. Optionally edit the planning and system prompts under **Prompts**. They
   auto-save as configuration; empty fields use the built-in defaults.
3. Use **Regenerate plan with AI** and review the unsaved preview, planning
   summary and any unallocated hours.
4. Use **Save forecast** to apply a complete plan, or **Discard preview** to
   leave stored entries unchanged.

Saving replaces non-vacation entries from today through the end of the displayed
month. Past bookings, vacation and other months remain unchanged. Project/week
totals are preserved; AI planning does not create extra hours or move them
between weeks. Saved months keep their stored daily distribution on reload.

Holidays and vacation reduce available capacity without double-counting
overlapping absences. In this calendar, vacation is an absence rather than
booked work. The regular baseline is 8 hours per day; AI previews may exceed it
and display overtime warnings. Invalid responses or unallocated hours prevent
saving. Previews expire after 30 minutes or a restart and must be regenerated
when source data changes.

**Data sent to AI:** the planning request includes project metadata, weekly
forecast totals, holiday/vacation availability and the last 84 days of actual
daily bookings. Unlike the goal chat, it includes individual daily entries.
Credentials and unrelated settings are not included in the planning context.

## Defaults for new data

| Setting | Default |
|---------|---------|
| Application language | `de` (German); select English in Settings |
| Federal state for holidays | `SN` (Saxony) |
| Weekly target | 40 hours |
| Gross fiscal-year hours | Weekdays × 8 hours |
| Vacation | 30 days |
| Public holidays | Calculated for the selected state |
| Standard tasks | 250 hours |

The fiscal-year goal is derived from gross hours minus vacation, public
holidays and standard tasks; it is not a separate target setting. Daily entries
before today count as booked hours, while today and later count as forecast.

## Screenshots and demo

The [generated gallery](https://daknoblo.github.io/forecast-tool/screenshots.html)
includes every discovered application navigation page and the dashboard's
supported horizon views. All views are rebuilt on each documentation build.
The [static demo](https://daknoblo.github.io/forecast-tool/demo/index.html)
contains generated data only and cannot save changes or call AI.

| View | Preview |
|------|---------|
| Dashboard: utilization, capacity and budgets | [Screenshot](https://daknoblo.github.io/forecast-tool/screenshots/dashboard.png) |
| Monthly planning: projects, absences and weekly capacity | [Screenshot](https://daknoblo.github.io/forecast-tool/screenshots/month.png) |
| Goals: fiscal-year targets and progress | [Screenshot](https://daknoblo.github.io/forecast-tool/screenshots/goal.png) |
| Projects: budgets, carry-over and burn-down | [Screenshot](https://daknoblo.github.io/forecast-tool/screenshots/projects.png) |
