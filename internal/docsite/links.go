package docsite

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// ValidateSite checks generated local links, images and documentation anchors.
// Demo anchors may refer to a different month folded into the captured month,
// so only documentation-to-documentation fragments are required to exist.
func ValidateSite(root string, withScreenshots bool) error {
	type reference struct{ source, target string }
	var refs []reference
	ids := map[string]map[string]bool{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(file) != ".html" {
			return nil
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		doc, err := html.Parse(strings.NewReader(string(content)))
		if err != nil {
			return err
		}
		ids[relative] = map[string]bool{}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			for _, attr := range n.Attr {
				switch attr.Key {
				case "id":
					ids[relative][attr.Val] = true
				case "href", "src":
					refs = append(refs, reference{relative, attr.Val})
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		return nil
	})
	if err != nil {
		return err
	}
	for _, ref := range refs {
		u, err := url.Parse(ref.target)
		if err != nil {
			return fmt.Errorf("%s: invalid URL %q: %w", ref.source, ref.target, err)
		}
		if u.IsAbs() || u.Host != "" {
			continue
		}
		target := ref.source
		if u.Path != "" {
			target = path.Clean(path.Join(path.Dir(ref.source), u.Path))
		}
		if strings.HasPrefix(u.Path, "/") || target == ".." || strings.HasPrefix(target, "../") {
			return fmt.Errorf("%s: link escapes site: %s", ref.source, ref.target)
		}
		if !withScreenshots && strings.HasPrefix(target, "screenshots/") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target))); err != nil {
			return fmt.Errorf("%s: broken link %s: %w", ref.source, ref.target, err)
		}
		if u.Fragment != "" && strings.HasSuffix(target, ".html") &&
			!strings.HasPrefix(ref.source, "demo/") && !strings.HasPrefix(target, "demo/") &&
			(withScreenshots || target != "screenshots.html") && !ids[target][u.Fragment] {
			return fmt.Errorf("%s: missing anchor in %s", ref.source, ref.target)
		}
	}
	return nil
}
