package rte

import (
	"net/url"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte/config"
)

// Connection URLs can contain both useful addresses and private credentials.
// Never include user info, queries or fragments in their public previews.
func publicPreviews(fields []manifest.ConfigField, view config.View) map[string]string {
	result := map[string]string{}
	for _, field := range fields {
		if !field.Secret || (field.Format != "proxy" && field.Format != "url") {
			continue
		}
		var value string
		if view.Get(field.Name, &value) != nil {
			continue
		}
		parsed, err := url.Parse(value)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			result[field.Name] = parsed.Scheme + "://" + parsed.Host
			if field.Format == "url" {
				result[field.Name] += parsed.EscapedPath()
			}
		}
	}
	return result
}
