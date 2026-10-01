# forecast-tool

[![CI](https://github.com/daknoblo/forecast-tool/actions/workflows/ci.yml/badge.svg)](https://github.com/daknoblo/forecast-tool/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/daknoblo/forecast-tool)](https://github.com/daknoblo/forecast-tool/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/daknoblo/forecast-tool)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![GHCR](https://img.shields.io/badge/ghcr.io-forecast--tool-blue?logo=docker)](https://github.com/daknoblo/forecast-tool/pkgs/container/forecast-tool)

A lightweight, single-user forecast tool built with Go and a server-rendered
web interface. Plan project hours, track budgets and utilization, and compare
booked and forecast hours against fiscal-year goals.

Data lives in `appdata/data.json`; no database is required. The fiscal-year
start month is configurable, and public holidays follow the selected German
federal state. Run on a private network or behind an authenticated reverse proxy.

**[Documentation and static demo](https://daknoblo.github.io/forecast-tool/)**
| **[Generated screenshots](https://daknoblo.github.io/forecast-tool/screenshots.html)**

**Container:** [GHCR image](https://github.com/daknoblo/forecast-tool/pkgs/container/forecast-tool)
· [Compose file](docker-compose.yml) · [Deployment guide](docs/DEPLOYMENT.md)

## Features at a glance

- **Projects and budgets:** track assignment budgets, booking windows and
  carry-over between fiscal years.
- **Monthly planning:** review daily project hours, vacation, public holidays
  and weekly capacity in one calendar.
- **Forecasts and goals:** compare booked and planned hours, utilization and
  fiscal-year progress, including imported ESXP Forecast Accuracy.
- **Optional AI assistance:** generate plans for review before saving, or ask
  read-only questions about your data in chat.
- **Private mode:** show generated sample data instead of your real figures
  when sharing your screen.
- **German and English:** switch the interface language in Settings without
  changing your project names or custom content.
- **Simple self-hosting:** run a container with a single JSON data file,
  export your data or synchronize hours through the token-protected HTTP API.

## A look inside

| Dashboard | Monthly planning |
|-----------|------------------|
| [![Dashboard showing utilization, project budgets and fiscal-year progress](https://daknoblo.github.io/forecast-tool/screenshots/dashboard.png)](https://daknoblo.github.io/forecast-tool/screenshots/dashboard.png) | [![Monthly calendar showing project hours, vacation and weekly capacity](https://daknoblo.github.io/forecast-tool/screenshots/month.png)](https://daknoblo.github.io/forecast-tool/screenshots/month.png) |
| See where your hours go and how your forecast compares with capacity and goals. | See which projects fill each day, where absences reduce capacity and where warnings need attention. |

Click either preview for the full-size image. Both show the English interface
with generated sample data and are refreshed by the documentation pipeline.
Explore the [read-only demo](https://daknoblo.github.io/forecast-tool/demo/index.html)
or the [complete screenshot gallery](https://daknoblo.github.io/forecast-tool/screenshots.html).

## Documentation

- [Features and monthly planning](docs/FEATURES.md)
- [Deployment, configuration and security](docs/DEPLOYMENT.md)
- [AI chat and planning configuration](docs/AI.md)
- [HTTP API, Forecast Accuracy and Scout setup](docs/API.md)
- [Development and releases](docs/DEVELOPMENT.md)
- [Architecture and design](docs/PLAN.md)
- [Documentation builds, screenshots and pull-request previews](docs/DOCSITE.md)

Detailed documentation is maintained in English. The demo and screenshots are
rebuilt from generated sample data; the demo is read-only, not a live planning
instance.

## License

Released under the [MIT License](LICENSE).
