package consolebot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TestPermissionsAndLifecycle(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {allowedField: []string{"1", "1", "2"}}})
	require.NoError(t, err)
	for _, status := range host.Start(ctx) {
		require.Equal(t, rte.Running, status.State, status.Detail)
	}
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	value, err := host.Resolve(ports.ConsoleName)
	require.NoError(t, err)
	console := value.(ports.Console)
	require.True(t, console.Allowed(types.DefaultAccount, 1))
	require.False(t, console.Allowed("other", 1))
	require.False(t, console.Allowed(types.DefaultAccount, 3))
	require.Error(t, host.Reconfigure(ctx, ID, map[string]any{allowedField: []string{"-1"}}))
	require.True(t, console.Allowed(types.DefaultAccount, 1))
	require.NoError(t, host.Reconfigure(ctx, ID, map[string]any{allowedField: []string{"3"}}))
	require.False(t, console.Allowed(types.DefaultAccount, 1))
	require.True(t, console.Allowed(types.DefaultAccount, 3))
	require.NoError(t, host.Stop(ctx))
	require.False(t, console.Allowed(types.DefaultAccount, 3))
}

func TestCommandCatalog(t *testing.T) {
	service := &Service{}
	menu := service.Commands()
	require.Len(t, menu, 18)
	seen := map[string]bool{}
	for _, command := range menu {
		require.False(t, seen[command.Name], command.Name)
		seen[command.Name] = true
		require.NotEmpty(t, command.Description)
		require.True(t, service.PrivateCommand(command.Name), command.Name)
	}
	menu[0].Name = "modified"
	require.Equal(t, "start", service.Commands()[0].Name)
	for _, alias := range []string{"downloads_help", "aria2_help", "internal", "internal_start_all"} {
		require.True(t, service.PrivateCommand(alias), alias)
	}
	require.False(t, service.PrivateCommand("unknown"))
	require.False(t, service.PrivateCommand(""))
}
