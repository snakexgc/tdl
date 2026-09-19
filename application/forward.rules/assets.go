package forwardrules

import (
	"embed"
	"io/fs"
)

//go:embed assets
var resources embed.FS

func Assets() fs.FS {
	result, _ := fs.Sub(resources, "assets")
	return result
}
