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
