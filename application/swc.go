// Package application composes components explicitly, without init registries.
package application

import (
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func Registry(commandSets ...[]types.ConsoleCommand) (*rte.Registry, error) {
	definitions, err := definitions(commandSets...)
	if err != nil {
		return nil, err
	}
	registry := rte.NewRegistry()
	for _, definition := range definitions {
		if definition.Factory == nil {
			continue
		}
		if err := registry.Register(definition.Manifest, definition.Factory); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
