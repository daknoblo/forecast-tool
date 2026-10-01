# Documentation builds and previews

The public site at <https://daknoblo.github.io/forecast-tool/> is generated from
this repository. The root [README](../README.md) is a brief overview; detailed
English documentation lives in thematic pages under `docs/`.

**Prose is maintained manually.** The build does not infer feature descriptions
from code. It automatically discovers Markdown documents, generates navigation,
and regenerates UI screenshots and a static demo from the actual application.
New features on existing pages appear in the next capture; new navigation pages
are discovered without extending a route list.

## Local build

Install the screenshot prerequisites once using Node 22 (as in CI):

```bash
cd tools/screenshots
npm ci
npx playwright install chromium
cd ../..
go test ./internal/docsite ./cmd/docsite
go run ./cmd/docsite -out site -require-screenshots
```

Open `site/index.html` or serve that directory with a local static HTTP server.
The output must not already exist: the command never deletes a user-supplied
directory. Choose a fresh output path for subsequent builds. Generated files
belong in the ignored `site/` directory or an explicitly chosen temporary
directory, never in source control.

## What is generated

1. **Sample data:** deterministic data anchored on the current UTC day, with
   five assignments, a vacation project, carry-over, booked/forecast hours,
   vacation blocks, public holidays for Saxony and Forecast Accuracy. Real
   `appdata` is never read.
2. **Navigation discovery:** start at `/` on the loopback demo server and follow
   root-relative links inside HTML `nav` elements. Only local navigation paths
   are accepted; exports, API routes, static assets, private-mode actions,
   downloads, external links and redirects are excluded. Discovery is limited
   to 64 pages and fails on filename collisions or broken page responses.
   Query strings are normalized and receive stable filenames. Content links,
   pagination and forms are not crawled.
3. **Static demo:** discovered pages plus the seven dashboard horizon views.
   Captured navigation and fragment links are rewritten to local files.
   Uncaptured query variants fall back to the captured page, if any.
   Unavailable internal links are disabled. Scripts are removed, controls are
   disabled, and a restrictive content policy blocks scripts, form submission
   and network requests. This is not an interactive planning instance.
4. **Screenshots:** every discovered page and horizon is freshly captured at
   1440 px width, scale factor 2, UTC and a fixed browser locale. A few explicit
   close-ups and the private-mode example supplement the full-page captures.
   UI labels come from the application; gallery and documentation chrome are
   English.
5. **Markdown:** the root README and **all** `.md` files recursively under
   `docs/` become HTML pages and appear in the site navigation. Each document
   must start with a `# Title`. Paths are lowercased and `.md` becomes `.html`;
   the `docs/` prefix is dropped (`docs/API.md` becomes `api.html`, and
   `docs/guides/Intro.md` becomes `guides/intro.html`). Case collisions,
   reserved output names and symlinks fail the build.

Relative Markdown links resolve from the **source document's directory** and
retain query strings and fragments. Links to other discovered documents point
to their HTML pages; other repository files link to GitHub. Images from the
repository use raw-content URLs. Absolute URLs, protocol-relative URLs, email
links and same-page fragments are preserved. Links to undiscovered Markdown
inside `docs/` fail rather than silently producing a broken page.
Before publishing the output, the builder checks local page/image links and
documentation fragments. Demo fragments are preserved, but not required to
exist when a different month has been folded into the captured month.

## Safety

The builder uses a fresh temporary store and removes inherited `AZURE_*`,
`FORECAST_*` and legacy `DATA_DIR` variables while running the demo. AI is
unconfigured, outbound HTTP connections are blocked, and the loopback handler
rejects writes, exports and API reads. Playwright also blocks external requests
and writes. No real AI service or private data is needed for a build.

The command builds in a dedicated temporary staging directory, then renames it
to the requested output only on success. Temporary demo data and staging files
are cleaned up. A failed required screenshot capture fails the build.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-out` | `site` | New output directory; existing paths are refused |
| `-repo` | `.` | Repository root holding README and `docs/` |
| `-capture` | `tools/screenshots/capture.mjs` | Playwright helper script |
| `-screenshots` | `true` | Capture screenshots |
| `-require-screenshots` | `false` | Fail rather than warn if capture fails; enabled in CI |
| `-language` | `de` | Seed `settings.language` with `de` or `en` for the demo and captures |

For a quick prose-only iteration, use `-screenshots=false` with a fresh output
path. The gallery will be empty, and explicit screenshot links in prose will
not have local images. Without `-require-screenshots`, a capture failure prints
a warning and removes partial images before continuing. Do not use that mode
to approve a visual change.

## Adding or changing features

- Update the appropriate English document and sample data where needed.
- Put a new public application page in the app's semantic `nav`; the build
  discovers it and captures it automatically.
- For a feature reachable only through a particular control/state, add an
  explicit read-only view or close-up to the docsite builder. Discovery does not
  exercise writes, dialogs, AI plans or arbitrary route parameters.
- Add new thematic documents anywhere under `docs/`; no Go page list changes
  are needed. Keep source links relative to the containing document.
- Review all screenshots in the pull-request artifact. Automatic capture is
  not a substitute for checking whether the sample data demonstrates a feature.

## Pull requests and publishing

[The Pages workflow](../.github/workflows/pages.yml) runs on every pull request,
push to `main`, and manual dispatch. All runs build screenshots and test the
documentation builder. The `docs-preview-<commit>` artifact contains the
complete English-UI site (`site/`) and German-UI site (`site-de/`) and is
retained for 14 days. Documentation prose remains English in both. Download and
extract it to review each `index.html`, `screenshots.html` and `demo/` locally.
The published site uses English UI screenshots; both languages are rebuilt and
available for review on every run.

Pull requests only upload the preview artifact: they cannot deploy Pages and
receive no Pages or OIDC write permissions. Only `main` builds upload the
Pages deployment artifact and run the separate deployment job. One-time setup:
repository **Settings → Pages → Source: GitHub Actions**.
