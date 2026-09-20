package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte/configtest"
)

func TestProductionCommandsExcludeRetiredAliasesAndDisabledOwners(t *testing.T) {
	ctx := context.Background()
	commands, err := ConsoleCommands(ctx, nil)
	require.NoError(t, err)
	require.Len(t, commands, 18)
	seen := map[string]bool{}
	for _, command := range commands {
		require.NotEmpty(t, command.Owner)
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			require.False(t, seen[name], name)
			seen[name] = true
		}
	}
	for _, alias := range []string{"downloads_help", "aria2_help", "internal", "internal_start_all"} {
		require.False(t, seen[alias], alias)
	}
	catalog, err := Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "download.control", nil)
	require.NoError(t, err)
	store := configtest.NewStore()
	require.NoError(t, store.Save(ctx, "download.control", false, view))
	commands, err = ConsoleCommands(ctx, store)
	require.NoError(t, err)
	for _, command := range commands {
		require.NotEqual(t, "download.control", command.Owner)
	}
}
