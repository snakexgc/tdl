package accounttelegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const (
	testAPIID   = "api_id"
	testAPIHash = "api_hash"
	testBuiltin = "builtin"
)

func TestCredentialOverrideAndRestore(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	value, err := host.Resolve(ports.TelegramCredentialsName)
	require.NoError(t, err)
	service := value.(ports.TelegramCredentials)
	for _, legacy := range []string{testBuiltin, "desktop"} {
		resolved, err := service.Resolve(ctx, types.DefaultAccount, legacy)
		require.NoError(t, err)
		expected, err := Preset(legacy)
		require.NoError(t, err)
		require.Equal(t, expected, resolved.App)
	}
	settings := map[string]any{testAPIID: 12345, testAPIHash: "private-hash"}
	require.NoError(t, host.Reconfigure(ctx, ID, settings))
	resolved, err := service.Resolve(ctx, types.DefaultAccount, "desktop")
	require.NoError(t, err)
	require.Equal(t, 12345, resolved.App.AppID)
	require.Equal(t, "private-hash", resolved.App.AppHash)
	require.Error(t, host.Reconfigure(ctx, ID, map[string]any{testAPIID: 54321}))
	unchanged, err := service.Resolve(ctx, types.DefaultAccount, "desktop")
	require.NoError(t, err)
	require.Equal(t, resolved, unchanged)
	settings["use_builtin"] = true
	require.NoError(t, host.Reconfigure(ctx, ID, settings))
	resolved, err = service.Resolve(ctx, types.DefaultAccount, "desktop")
	require.NoError(t, err)
	expected, err := Preset("desktop")
	require.NoError(t, err)
	require.Equal(t, expected, resolved.App)
	_, err = service.Resolve(ctx, "other", "desktop")
	require.Error(t, err)
}

func TestCredentialValidation(t *testing.T) {
	for _, settings := range []types.TelegramCredentialsConfig{
		{APIID: -1}, {APIID: 123}, {APIHash: "private-hash"}, {BuiltinPreset: "unknown"},
	} {
		require.Error(t, Validate(settings))
	}
	require.NoError(t, Validate(types.TelegramCredentialsConfig{}))
}
