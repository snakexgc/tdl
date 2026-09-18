package application

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
)

func ConsoleCommands(ctx context.Context, store *config.Store) ([]types.ConsoleCommand, error) {
	catalog, err := Catalog()
	if err != nil {
		return nil, err
	}
	commands := []types.ConsoleCommand{}
	for _, definition := range catalog.Definitions() {
		if store != nil {
			document, err := store.Load(ctx, definition.Manifest.ID)
			if err != nil {
				return nil, err
			}
			if !document.Enabled {
				continue
			}
		}
		for _, command := range definition.Manifest.Commands {
			command.Owner = definition.Manifest.ID
			commands = append(commands, command)
		}
	}
	return commands, nil
}
