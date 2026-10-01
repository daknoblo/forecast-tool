# Development and releases

## Local development

Use the Go version specified in [go.mod](../go.mod). The application uses
server-rendered Go templates and SVG; there is no frontend bundler.

```bash
go run ./cmd/server
go test ./...
go vet ./...
go build ./...
```

The server defaults to port 8080 and `appdata/`. Use `FORECAST_DATA_DIR` to
select a separate development data directory; never point tests or demos at
your private planning data. See [configuration](DEPLOYMENT.md#configuration).

Browser regression tests use the pinned Playwright dependency under
`tools/screenshots` and mock AI locally:

```bash
cd tools/screenshots
npm ci
npx playwright install chromium
cd ../..
FORECAST_BROWSER_TESTS=1 go test ./internal/web -run TestBrowserRegression -count=1
```

See [architecture and design](PLAN.md) for the domain model and source layout.
The [documentation build guide](DOCSITE.md) explains generated screenshots,
navigation discovery and preview artifacts.

## Language settings

`models.Settings.Language` is serialized as `settings.language`, with German
(`de`) as the default and English (`en`) as the alternative. Translate
application-owned text only; do not translate stored user content.

Rendered application pages set both the HTML `lang` attribute and the
`Content-Language` response header to the selected language. Localization
covers dates, statuses, charts, UI errors and default AI prompts; custom
content is unchanged. External API errors deliberately remain German for
compatibility.

The Settings page uses a **separate auto-save form** for language changes:
`POST /settings` with form fields `section=language` and `language=de` or
`language=en`. The language selector requests a reload after saving. Keeping
this form separate avoids submitting unrelated settings with a language change.
External integrations should use the bearer-authenticated
[partial settings update](API.md#put-apiv1settings--global-settings), not the
same-origin UI form endpoint.

The [documentation builder](DOCSITE.md) seeds the generated demo store with
the selected language before starting the application. CI captures every
documentation screenshot in both German and English.

## CI/CD: checks and container release

- [CI](../.github/workflows/ci.yml): formatting, vet, lint, vulnerability checks,
  race-enabled Go tests, browser regressions and a static build.
- [Release](../.github/workflows/release.yml): gated by vet, race tests and browser
  regressions; publishes multi-architecture GHCR images with SBOM, provenance
  and keyless signatures. Trivy scans report findings after publication.
- [CodeQL](../.github/workflows/codeql.yml): runs on pushes, pull requests and a
  weekly schedule.
- [Pages](../.github/workflows/pages.yml): builds and tests documentation, demo
  and screenshots on pull requests; uploads a reviewable artifact without
  publishing. Pushes to `main` also publish to GitHub Pages.

Main-branch images use `latest` and `sha-<short>` tags. A version tag also
publishes versioned images (for example, `1.2.3` and `1.2`) and a GitHub release:

```bash
git tag -a v1.2.3 -m "v1.2.3" -m "What changed ..."
git push origin v1.2.3
```

Workflows use the built-in `GITHUB_TOKEN`; no publishing PAT is needed. For
public pulls, make the GHCR package public. Private packages require a
`docker login ghcr.io` with a token that has `read:packages` access.
