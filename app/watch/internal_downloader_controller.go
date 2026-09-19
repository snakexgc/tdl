package watch

import (
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/internal/core/storage"
)

// Compatibility name for callers of the local component's control port.
type InternalDownloadController = local.Controller

func NewInternalDownloadController(kvd storage.Storage) *InternalDownloadController {
	return local.NewController(newInternalTaskStore(kvd))
}
