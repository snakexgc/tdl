package application

import (
	"errors"
	"io/fs"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

// WebAssets composes component-owned resources at their existing public paths.
func WebAssets(catalogs ...*rte.Catalog) fs.FS {
	catalog := webCatalog(catalogs)
	sources := assetSources{}
	for _, definition := range catalog.Definitions() {
		if definition.Assets != nil {
			sources = append(sources, definition.Assets)
		}
	}
	return sources
}

func WebRoutes(catalogs ...*rte.Catalog) []types.WebRoute {
	catalog := webCatalog(catalogs)
	routes := []types.WebRoute{}
	for _, definition := range catalog.Definitions() {
		for _, route := range definition.Routes {
			route.Owner = definition.Manifest.ID
			routes = append(routes, route)
		}
	}
	return routes
}

func webCatalog(catalogs []*rte.Catalog) *rte.Catalog {
	if len(catalogs) > 0 && catalogs[0] != nil {
		return catalogs[0]
	}
	catalog, err := Catalog()
	if err != nil {
		panic(err)
	}
	return catalog
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
