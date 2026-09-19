package watch

import (
	"path/filepath"

	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
)

const internalDownloadFallbackDirName = "downloads"

func prepareInternalOutputRoot(cfg *config.Config) (root string, fallback bool, err error) {
	return prepareLocalRoot(cfg.Downloader.LocalRoot)
}

func prepareLocalRoot(configured string) (root string, fallback bool, err error) {
	return local.PrepareRoot(configured, filepath.Join(consts.HomeDir, internalDownloadFallbackDirName))
}
