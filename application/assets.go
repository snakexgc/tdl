package application

import (
	"errors"
	"io/fs"

	account "github.com/snakexgc/tdl/application/account.telegram"
	download "github.com/snakexgc/tdl/application/download.control"
	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/application/forwarder"
	panel "github.com/snakexgc/tdl/application/panel.webui"
	update "github.com/snakexgc/tdl/application/update.self"
	"github.com/snakexgc/tdl/interfaces/types"
)

// WebAssets composes component-owned resources at their existing public paths.
func WebAssets() fs.FS {
	return assetSources{panel.Assets(), account.Assets(), download.Assets(), aria2.Assets(), forwarder.Assets(), update.Assets()}
}

func WebRoutes() []types.WebRoute {
	var routes []types.WebRoute
	for _, group := range [][]types.WebRoute{panel.Routes(), account.Routes(), download.Routes(), aria2.Routes(), forwarder.Routes(), update.Routes()} {
		routes = append(routes, group...)
	}
	return routes
}

type assetSources []fs.FS

func (sources assetSources) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	for _, source := range sources {
		file, err := source.Open(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
