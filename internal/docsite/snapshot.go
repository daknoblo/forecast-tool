package docsite

import (
	"crypto/sha256"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Page is one HTML document of the static demo snapshot: the URL to fetch from
// the running server and the file it is written to.
type Page struct {
	URL   string // path incl. query, e.g. "/?sankey=fy"
	File  string // output file name, e.g. "dashboard-fy.html"
	Title string // label used in the demo navigation of the docs site
}

// DiscoverDemoPages follows only navigation links, not forms, exports, APIs or
// arbitrary content links. Discovery is bounded and redirects are never followed.
func DiscoverDemoPages(baseURL string) ([]Page, error) {
	pages := []Page{{URL: "/", File: "index.html", Title: "Dashboard"}}
	seen := map[string]bool{"/": true}
	files := map[string]string{"index.html": "/"}
	for i := 0; i < len(pages); i++ {
		body, err := fetch(demoClient(), baseURL+pages[i].URL)
		if err != nil {
			return nil, err
		}
		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		var walk func(*html.Node, bool)
		walk = func(n *html.Node, inNav bool) {
			inNav = inNav || n.Type == html.ElementNode && n.Data == "nav"
			if inNav && n.Type == html.ElementNode && n.Data == "a" {
				var href string
				download := false
				for _, a := range n.Attr {
					if a.Key == "href" {
						href = a.Val
					}
					download = download || a.Key == "download"
				}
				u, parseErr := url.Parse(href)
				if !download && parseErr == nil && safeDemoURL(u) {
					key := normalizeURL(href)
					if !seen[key] {
						seen[key] = true
						file := demoFilename(u)
						if other, exists := files[file]; exists && other != key {
							err = fmt.Errorf("demo filename collision: %s and %s", other, key)
						}
						files[file] = key
						pages = append(pages, Page{URL: key, File: file, Title: strings.Join(strings.Fields(nodeText(n)), " ")})
					}
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child, inNav)
			}
		}
		walk(doc, false)
		if err != nil {
			return nil, err
		}
		if len(pages) > 64 {
			return nil, fmt.Errorf("demo navigation exceeds 64 pages")
		}
	}
	// Additional states of an existing page are deliberately explicit; do not
	// crawl month/year pagination, which could expand without a useful bound.
	for _, key := range []string{"1w", "2w", "4w", "2m", "3m", "6m", "fy"} {
		if seen["/?sankey="+key] {
			continue
		}
		file := "dashboard-" + key + ".html"
		if _, exists := files[file]; exists {
			return nil, fmt.Errorf("demo filename is reserved for a dashboard view: %s", file)
		}
		pages = append(pages, Page{
			URL:   "/?sankey=" + key,
			File:  file,
			Title: "Dashboard (" + key + ")",
		})
	}
	return pages, nil
}

var demoPathRe = regexp.MustCompile(`^/(?:[A-Za-z0-9_-]+/?)*$`)

func safeDemoURL(u *url.URL) bool {
	if u == nil || u.IsAbs() || u.Host != "" || u.User != nil || u.RawPath != "" || !demoPathRe.MatchString(u.Path) {
		return false
	}
	for _, prefix := range []string{"/api", "/export", "/healthz", "/static", "/private"} {
		if u.Path == prefix || strings.HasPrefix(u.Path, prefix+"/") {
			return false
		}
	}
	return true
}

func demoFilename(u *url.URL) string {
	name := strings.Trim(u.Path, "/")
	if name == "" {
		name = "index"
	}
	name = strings.ReplaceAll(name, "/", "--")
	if u.RawQuery != "" {
		sum := sha256.Sum256([]byte(u.Query().Encode()))
		name += fmt.Sprintf("-%x", sum[:8])
	}
	return name + ".html"
}

func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var text strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text.WriteString(nodeText(c))
	}
	return text.String()
}

func demoClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Snapshot fetches every page from the running server and writes a static,
// self-contained copy to outDir: links are rewritten to the local file names,
// the stylesheet is copied along and all writing interactions are neutralised.
func Snapshot(baseURL, outDir string, pages []Page) error {
	if err := os.MkdirAll(filepath.Join(outDir, "static"), 0o750); err != nil {
		return err
	}
	client := demoClient()

	// Map every captured URL to its file name so links between pages keep working.
	byURL := make(map[string]string, len(pages))
	for _, p := range pages {
		byURL[normalizeURL(p.URL)] = p.File
	}

	assets := map[string]bool{}
	for _, p := range pages {
		body, err := fetch(client, baseURL+p.URL)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", p.URL, err)
		}
		html, refs := rewrite(string(body), byURL)
		for _, a := range refs {
			assets[a] = true
		}
		if err := os.WriteFile(filepath.Join(outDir, p.File), []byte(html), 0o600); err != nil {
			return err
		}
	}

	for asset := range assets {
		body, err := fetch(client, baseURL+asset)
		if err != nil {
			return fmt.Errorf("snapshot asset %s: %w", asset, err)
		}
		name := path.Base(strings.SplitN(asset, "?", 2)[0])
		if err := os.WriteFile(filepath.Join(outDir, "static", name), body, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func fetch(client *http.Client, target string) ([]byte, error) {
	// The target always points at the demo server this process just started.
	resp, err := client.Get(target) //nolint:gosec // local demo server started by this tool
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") && !strings.Contains(target, "/static/") {
		return nil, fmt.Errorf("expected HTML from %s", target)
	}
	return io.ReadAll(resp.Body)
}

// attrRe matches the URL-carrying attributes of the application's templates.
// Parsing the whole document is unnecessary: the markup is generated by
// html/template and always quotes its attributes with double quotes.
var attrRe = regexp.MustCompile(`(href|action|src)="([^"]*)"`)

var bodyOpenRe = regexp.MustCompile(`(?i)<body[^>]*>`)
var scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
var controlRe = regexp.MustCompile(`(?i)<(input|button|select|textarea)\b`)

// rewrite points every internal link at its snapshot file and returns the
// static assets the page referenced.
func rewrite(html string, byURL map[string]string) (string, []string) {
	var assets []string
	html = scriptRe.ReplaceAllString(html, "")
	html = controlRe.ReplaceAllString(html, "<$1 disabled")
	html = attrRe.ReplaceAllStringFunc(html, func(m string) string {
		g := attrRe.FindStringSubmatch(m)
		attr, val := g[1], stdhtml.UnescapeString(g[2])
		if attr == "action" {
			return `action="#"`
		}
		if !strings.HasPrefix(val, "/") {
			return m // external, anchor or already relative
		}
		if strings.HasPrefix(val, "/static/") && !strings.Contains(val, "..") {
			assets = append(assets, val)
			return attr + `="static/` + path.Base(strings.SplitN(val, "?", 2)[0]) + `"`
		}
		if file, ok := byURL[normalizeURL(val)]; ok {
			return snapshotLink(attr, file, val)
		}
		if file, ok := byURL[normalizeURL(fallbackURL(val))]; ok {
			return snapshotLink(attr, file, val)
		}
		return attr + `="#"`
	})
	html = strings.Replace(html, "<head>", "<head>"+demoHead, 1)
	html = bodyOpenRe.ReplaceAllStringFunc(html, func(m string) string {
		return m + demoBanner
	})
	return html, assets
}

func snapshotLink(attr, file, original string) string {
	u, _ := url.Parse(original)
	target := &url.URL{Path: file}
	if u != nil {
		target.Fragment = u.Fragment
	}
	return attr + `="` + stdhtml.EscapeString(target.String()) + `"`
}

// fallbackURL maps a link that was not captured to the closest page that was,
// e.g. a link to another month to the captured monthly calendar.
func fallbackURL(val string) string {
	u, err := url.Parse(val)
	if err == nil {
		return u.Path
	}
	return ""
}

// normalizeURL makes two spellings of the same link comparable (sorted query).
func normalizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	p := u.Path
	if q := u.Query().Encode(); q != "" {
		return p + "?" + q
	}
	return p
}

const demoHead = `
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; form-action 'none'; base-uri 'none'">
<style>
.demo-banner{position:sticky;top:0;z-index:50;display:flex;flex-wrap:wrap;align-items:center;gap:.75rem;
  padding:.5rem 1rem;background:#0f172a;color:#e2e8f0;font-size:.85rem}
.demo-banner strong{color:#fff}
.demo-banner a{color:#93c5fd;text-decoration:none}
.demo-banner a:hover{text-decoration:underline}
.demo-banner .spacer{flex:1 1 auto}
</style>`

const demoBanner = `
<div class="demo-banner">
  <strong>Static demo</strong>
  <span>Generated sample data &ndash; controls and network actions are disabled.</span>
  <span class="spacer"></span>
  <a href="../index.html">Documentation</a>
  <a href="https://github.com/daknoblo/forecast-tool">GitHub</a>
</div>`
