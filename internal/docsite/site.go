package docsite

import (
	"bytes"
	"embed"
	"fmt"
	stdhtml "html"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
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

// markdownPage is a repository document rendered into the site.
type markdownPage struct {
	Source string // path relative to the repository root
	File   string // output file name
	Title  string
	Lead   string
	Hero   bool
}

// discoverMarkdownPages preserves the original top-level URLs while allowing
// nested documents. Symlinks and output collisions fail rather than publishing
// files outside the documentation tree or silently overwriting another page.
func discoverMarkdownPages(repo string) ([]markdownPage, error) {
	info, err := os.Lstat(filepath.Join(repo, "README.md"))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("README.md must be a regular file")
	}
	pages := []markdownPage{{
		Source: "README.md", File: "index.html", Title: "Overview", Hero: true,
		Lead: "Plan project hours, budgets and fiscal-year goals with a lightweight Go application.",
	}}
	seen := map[string]bool{"index.html": true, "screenshots.html": true}
	err = filepath.WalkDir(filepath.Join(repo, "docs"), func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("documentation symlink is not supported: %s", file)
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(file), ".md") {
			return nil
		}
		rel, err := filepath.Rel(repo, file)
		if err != nil {
			return err
		}
		source := filepath.ToSlash(rel)
		output := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(source, "docs/"), filepath.Ext(file))) + ".html"
		if seen[output] || strings.HasPrefix(output, "demo/") || strings.HasPrefix(output, "assets/") || strings.HasPrefix(output, "screenshots/") {
			return fmt.Errorf("documentation output collision: %s", source)
		}
		seen[output] = true
		src, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		title := strings.TrimSpace(strings.TrimPrefix(string(h1Re.Find(src)), "#"))
		if title == "" {
			return fmt.Errorf("%s must start with a level-one heading", source)
		}
		pages = append(pages, markdownPage{Source: source, File: output, Title: title})
		return nil
	})
	return pages, err
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
	Root        string
	HeadingID   string
}

// BuildSite renders the Markdown documents and the screenshot gallery into
// outDir. It expects the demo snapshot and the screenshots to be in place
// already, because the gallery links to them.
func BuildSite(repoRoot, outDir string, shots []Shot, demo []Page, generatedAt string) error {
	pages, err := discoverMarkdownPages(repoRoot)
	if err != nil {
		return err
	}
	links := make(map[string]string, len(pages))
	nav := []NavItem{{Label: "Overview", Href: "index.html"}, {Label: "Screenshots", Href: "screenshots.html"}, {Label: "Demo", Href: "demo/index.html"}}
	for _, p := range pages {
		links[p.Source] = p.File
		if !p.Hero {
			nav = append(nav, NavItem{Label: p.Title, Href: p.File})
		}
	}
	nav = append(nav, NavItem{Label: "GitHub", Href: RepoURL, External: true})
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

	for _, p := range pages {
		src, err := os.ReadFile(filepath.Join(repoRoot, p.Source)) // #nosec G304 -- discovered repository documents
		if err != nil {
			return fmt.Errorf("read %s: %w", p.Source, err)
		}
		var buf bytes.Buffer
		if err := md.Convert(src, &buf); err != nil {
			return fmt.Errorf("render %s: %w", p.Source, err)
		}
		rendered := buf.String()
		var headingID string
		if heading := renderedHeadingRe.FindStringSubmatch(rendered); len(heading) > 0 {
			headingID = stdhtml.UnescapeString(heading[1])
			rendered = strings.TrimPrefix(rendered, heading[0])
		}
		content, err := rewriteDocLinks(rendered, p, links)
		if err != nil {
			return fmt.Errorf("links in %s: %w", p.Source, err)
		}
		data := pageData{
			Title:       p.Title,
			Lead:        p.Lead,
			Hero:        p.Hero,
			Nav:         nav,
			Active:      p.File,
			Content:     template.HTML(content), // #nosec G203 -- rendered by goldmark with raw HTML disabled
			Shots:       shots,
			RepoURL:     RepoURL,
			GeneratedAt: generatedAt,
			Root:        strings.Repeat("../", strings.Count(p.File, "/")),
			HeadingID:   headingID,
		}
		if err := writePage(tpl, filepath.Join(outDir, p.File), data); err != nil {
			return err
		}
	}

	gallery := pageData{
		Title:       "Screenshots",
		Lead:        "Rebuilt from the application's navigation and generated sample data on every documentation build.",
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
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
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
var h1Re = regexp.MustCompile(`(?m)\A#\s+[^\n]*(?:\n|$)`)
var renderedHeadingRe = regexp.MustCompile(`\A<h1 id="([^"]+)">[^\n]*</h1>\n`)

var hrefRe = regexp.MustCompile(`(href|src)="([^"]*)"`)

// pagesPrefix is how the README references the published screenshots. Inside
// the site itself the relative copy is used, so the pages work before the site
// is deployed for the first time.
const pagesPrefix = "https://daknoblo.github.io/forecast-tool/"

func rewriteDocLinks(html string, page markdownPage, links map[string]string) (string, error) {
	var linkErr error
	root := strings.Repeat("../", strings.Count(page.File, "/"))
	result := hrefRe.ReplaceAllStringFunc(html, func(m string) string {
		g := hrefRe.FindStringSubmatch(m)
		attr, val := g[1], stdhtml.UnescapeString(g[2])
		if rest, ok := strings.CutPrefix(val, pagesPrefix); ok {
			if rest == "" {
				rest = "index.html"
			}
			return attr + `="` + stdhtml.EscapeString(root+rest) + `"`
		}
		u, err := url.Parse(val)
		if err != nil {
			linkErr = fmt.Errorf("invalid URL %q: %w", val, err)
			return m
		}
		if u.IsAbs() || u.Host != "" || u.Path == "" {
			return m
		}
		source := path.Clean(path.Join(path.Dir(page.Source), u.Path))
		if strings.HasPrefix(u.Path, "/") {
			source = path.Clean(strings.TrimPrefix(u.Path, "/"))
		}
		if source == ".." || strings.HasPrefix(source, "../") {
			linkErr = fmt.Errorf("link escapes repository: %q", val)
			return m
		}
		if target, ok := links[source]; ok {
			u.Path = root + target
		} else {
			if strings.EqualFold(path.Ext(source), ".md") && (source == "README.md" || strings.HasPrefix(source, "docs/")) {
				linkErr = fmt.Errorf("undiscovered Markdown target: %q", val)
				return m
			}
			base := RepoURL + "/blob/main/"
			if attr == "src" {
				base = "https://raw.githubusercontent.com/daknoblo/forecast-tool/main/"
			}
			target, _ := url.Parse(base)
			u.Scheme, u.Host, u.Path = target.Scheme, target.Host, target.Path+source
		}
		u.RawPath = ""
		return attr + `="` + stdhtml.EscapeString(u.String()) + `"`
	})
	return result, linkErr
}
