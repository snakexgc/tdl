package updater

import (
	"bytes"
	"html"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// GitHub release bodies are untrusted Markdown. Keep goldmark's safe defaults:
// raw HTML and dangerous URLs must not be enabled with html.WithUnsafe.
func renderReleaseNotes(source string) string {
	markdown := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var output bytes.Buffer
	if err := markdown.Convert([]byte(source), &output); err != nil {
		return "<pre>" + html.EscapeString(source) + "</pre>"
	}
	return output.String()
}
