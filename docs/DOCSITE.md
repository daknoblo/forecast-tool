# Documentation site, demo & screenshots

The public site at <https://daknoblo.github.io/forecast-tool/> is generated from
this repository. One command builds all of it:

```bash
go run ./cmd/docsite -out site
```

## What the build does

`cmd/docsite` starts the **real application** in-process against a generated
demo document and then derives everything from that running instance:

1. **Demo data** (`internal/docsite/demo.go`) – a deterministic document
   anchored on today: five assignments plus the automatic vacation project, a
   carry-over from the previous fiscal year, hours booked on every weekday from
   the fiscal-year start up to ten weeks ahead, two vacation blocks and public
   holidays for Saxony. There is no random source; the pattern comes from a hash
   of the date, so the same day always produces the same document.
2. **Static snapshot** (`snapshot.go`) – every GET page is fetched, its links
   are rewritten to the local file names, the stylesheet is copied along and a
   small script neutralises form submits and background saves. The result in
   `site/demo/` is a clickable copy of the application that needs no server.
3. **Screenshots** (`shots.go` + `tools/screenshots/capture.mjs`) – Playwright
   renders the shot list against the same instance at 1440 px width and a device
   scale factor of 2, with a fixed locale (`de-DE`) and time zone (UTC) so the
   images stay reproducible.
4. **Documentation** (`site.go`) – `README.md`, `docs/API.md` and `docs/PLAN.md`
   are rendered with goldmark (GFM) into the site layout, plus the generated
   screenshot gallery.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-out` | `site` | Output directory (wiped before the build) |
| `-repo` | `.` | Repository root holding `README.md` and `docs/` |
| `-capture` | `tools/screenshots/capture.mjs` | Playwright helper script |
| `-screenshots` | `true` | Capture screenshots at all |
| `-require-screenshots` | `false` | Fail instead of continuing without them (used in CI) |

## Prerequisites for the screenshots

```bash
cd tools/screenshots
npm ci
npx playwright install chromium
```

Without them, `go run ./cmd/docsite -out site` still succeeds: it prints a
warning and renders the site without the gallery. Use `-screenshots=false` to
skip the browser step deliberately.

## Adding a page or a screenshot

- A **new route** goes into `docsite.DemoPages` so it is part of the clickable
  snapshot. Links pointing at pages that were not captured fall back to the
  closest captured page (see `fallbackURL`), so nothing dead-ends.
- A **new screenshot** goes into `docsite.DemoShots`, with a German title and
  description – those become the gallery caption. `Selector` limits the capture
  to a single element (e.g. a chart card), otherwise `FullPage` captures the
  whole page.
- A **new Markdown document** goes into `markdownPages` in `site.go` and, if it
  should be reachable, into `nav`.

## Publishing

`.github/workflows/pages.yml` runs the same command on every push to `main` (and
on demand via *Run workflow*) and deploys the `site/` directory to GitHub Pages.
It needs *Settings → Pages → Source: **GitHub Actions*** to be enabled once.
