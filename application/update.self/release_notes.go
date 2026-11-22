package updater

import (
	"bytes"
	"html"

	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	goldmarkhtml "github.com/yuin/goldmark/v2/renderer/html"
)

// GitHub release bodies are untrusted Markdown. Keep goldmark's safe defaults:
// raw HTML and dangerous URLs must not be enabled with html.WithUnsafe.
func renderReleaseNotes(source string) string {
	markdown := parser.New(parser.WithExtensions(extension.NewGFMParser()))
	renderer := goldmarkhtml.New(goldmarkhtml.WithExtensions(extension.NewGFMHTMLRenderer()))
	document := markdown.ParseStringSource(source)
	var output bytes.Buffer
	if err := renderer.RenderStringSource(&output, source, document); err != nil {
		return "<pre>" + html.EscapeString(source) + "</pre>"
	}
	return output.String()
}
