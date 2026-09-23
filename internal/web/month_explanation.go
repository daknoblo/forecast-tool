package web

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

var monthExplanationMarkdown = goldmark.New(
	goldmark.WithRendererOptions(
		gmhtml.WithHardWraps(),
		renderer.WithNodeRenderers(util.Prioritized(explanationTextLinks{}, 100)),
	),
)

// AI links and images render as labels only: no navigation or remote loads.
type explanationTextLinks struct{}

func (explanationTextLinks) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	for _, kind := range []ast.NodeKind{ast.KindLink, ast.KindImage, ast.KindAutoLink} {
		reg.Register(kind, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
			if link, ok := node.(*ast.AutoLink); ok && entering {
				_, err := w.WriteString(template.HTMLEscapeString(string(link.Label(source))))
				return ast.WalkContinue, err
			}
			return ast.WalkContinue, nil
		})
	}
}

func renderMonthExplanation(text string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := monthExplanationMarkdown.Convert([]byte(text), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil // #nosec G203 -- goldmark disables raw HTML; links/images render as escaped text
}
