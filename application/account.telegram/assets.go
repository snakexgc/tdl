package accounttelegram

import (
	"embed"
	"io/fs"
)

//go:embed assets
var resources embed.FS

// Assets returns the component-owned UI resources with their public paths.
func Assets() fs.FS {
	result, _ := fs.Sub(resources, "assets")
	return result
}
