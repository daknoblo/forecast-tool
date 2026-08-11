package docsite

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

//go:embed assets/*
var assetFS embed.FS

// RepoURL is the canonical repository the site links back to.
const RepoURL = "https://github.com/daknoblo/forecast-tool"

// NavItem is one entry of the site navigation.
type NavItem struct {
	Label    string
	Href     string
	External bool
}

var nav = []NavItem{
	{Label: "Start", Href: "index.html"},
	{Label: "Screenshots", Href: "screenshots.html"},
	{Label: "Demo", Href: "demo/index.html"},
	{Label: "API", Href: "api.html"},
	{Label: "Plan", Href: "plan.html"},
	{Label: "Doku & Demo", Href: "docsite.html"},
	{Label: "GitHub", Href: RepoURL, External: true},
}

// markdownPage is a repository document rendered into the site.
type markdownPage struct {
	Source string // path relative to the repository root
	File   string // output file name
	Title  string
	Lead   string
	Hero   bool
}

var markdownPages = []markdownPage{
	{
		Source: "README.md", File: "index.html", Title: "Übersicht", Hero: true,
		Lead: "Schlankes Single-User-Forecast-Tool in Go: Projekte mit Stundenbudget, tagesgenauer Forecast, Fiskaljahr-Logik und serverseitig gerenderte Diagramme – ganz ohne Datenbank.",
	},
	{Source: "docs/API.md", File: "api.html", Title: "HTTP-API", Lead: "Die JSON-API unter /api/v1 für externe Clients."},
	{Source: "docs/PLAN.md", File: "plan.html", Title: "Plan", Lead: "Ziel, Architektur und Umsetzungsschritte des Projekts."},
	{Source: "docs/DOCSITE.md", File: "docsite.html", Title: "Doku & Demo", Lead: "Wie diese Website, die Demo und die Screenshots automatisch entstehen."},
}

// pageData is what assets/page.html.tmpl renders.
type pageData struct {
	Title       string
	Lead        string
	Hero        bool
	Nav         []NavItem
	Active      string
	Content     template.HTML
	Shots       []Shot
	DemoPages   []Page
	RepoURL     string
	GeneratedAt string
}

// BuildSite renders the Markdown documents and the screenshot gallery into
// outDir. It expects the demo snapshot and the screenshots to be in place
// already, because the gallery links to them.
func BuildSite(repoRoot, outDir string, shots []Shot, demo []Page, generatedAt string) error {
	tpl, err := template.ParseFS(assetFS, "assets/page.html.tmpl")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outDir, "assets"), 0o750); err != nil {
		return err
	}
	if err := copyAsset("assets/site.css", filepath.Join(outDir, "assets", "site.css")); err != nil {
		return err
	}
	// GitHub Pages must serve the files as they are, not through Jekyll.
	if err := os.WriteFile(filepath.Join(outDir, ".nojekyll"), nil, 0o600); err != nil {
		return err
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(gmhtml.WithXHTML()),
	)

	for _, p := range markdownPages {
		src, err := os.ReadFile(filepath.Join(repoRoot, p.Source)) // #nosec G304 -- fixed list of documents from this repository
		if err != nil {
			return fmt.Errorf("read %s: %w", p.Source, err)
		}
		var buf bytes.Buffer
		if err := md.Convert(stripFirstHeading(src), &buf); err != nil {
			return fmt.Errorf("render %s: %w", p.Source, err)
		}
		data := pageData{
			Title:       p.Title,
			Lead:        p.Lead,
			Hero:        p.Hero,
			Nav:         nav,
			Active:      p.File,
			Content:     template.HTML(rewriteDocLinks(buf.String())), // #nosec G203 -- rendered by goldmark with raw HTML disabled
			Shots:       shots,
			RepoURL:     RepoURL,
			GeneratedAt: generatedAt,
		}
		if err := writePage(tpl, filepath.Join(outDir, p.File), data); err != nil {
			return err
		}
	}

	gallery := pageData{
		Title:       "Screenshots",
		Lead:        "Automatisch aufgenommen aus einer Demo-Instanz mit Beispieldaten – bei jeder Änderung neu erzeugt.",
		Nav:         nav,
		Active:      "screenshots.html",
		Shots:       shots,
		DemoPages:   demo,
		RepoURL:     RepoURL,
		GeneratedAt: generatedAt,
	}
	return writePage(tpl, filepath.Join(outDir, "screenshots.html"), gallery)
}

func writePage(tpl *template.Template, path string, data pageData) error {
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "page.html.tmpl", data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

func copyAsset(name, dst string) error {
	f, err := assetFS.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

// h1Re matches the leading level-1 heading of a Markdown document; the site
// renders its own title, so the duplicate is dropped.
var h1Re = regexp.MustCompile(`(?m)\A#\s+[^\n]*\n`)

func stripFirstHeading(src []byte) []byte {
	return h1Re.ReplaceAll(src, nil)
}

// docLinks maps repository-relative links to their place on the site; anything
// else that points into the repository is sent to GitHub.
var docLinks = map[string]string{
	"docs/API.md":     "api.html",
	"docs/PLAN.md":    "plan.html",
	"docs/DOCSITE.md": "docsite.html",
}

var hrefRe = regexp.MustCompile(`(href|src)="([^"]*)"`)

// pagesPrefix is how the README references the published screenshots. Inside
// the site itself the relative copy is used, so the pages work before the site
// is deployed for the first time.
const pagesPrefix = "https://daknoblo.github.io/forecast-tool/"

func rewriteDocLinks(html string) string {
	return hrefRe.ReplaceAllStringFunc(html, func(m string) string {
		g := hrefRe.FindStringSubmatch(m)
		attr, val := g[1], g[2]
		if target, ok := docLinks[val]; ok {
			return attr + `="` + target + `"`
		}
		if rest, ok := strings.CutPrefix(val, pagesPrefix); ok {
			return attr + `="` + rest + `"`
		}
		if strings.HasPrefix(val, "http") || strings.HasPrefix(val, "#") || strings.HasPrefix(val, "mailto:") {
			return m
		}
		return attr + `="` + RepoURL + `/blob/main/` + strings.TrimPrefix(val, "./") + `"`
	})
}
