package rte_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/configtest"
)

func TestConfigurationShowsAddressesAndAllowedUsersWithoutCredentials(t *testing.T) {
	ctx := context.Background()
	catalog, err := rte.NewCatalog(rte.Definition{Scope: rte.AccountScope, Manifest: manifest.Manifest{
		ID: "settings", Config: []manifest.ConfigField{
			manifest.FormattedText("proxy", "Proxy", "", "proxy", true, true),
			manifest.FormattedText("rpc_url", "RPC", "", "url", true, true),
			manifest.Text("api_hash", "API", "", true, false),
			{Name: "allowed_users", Type: manifest.Strings, Default: []string{}},
		},
	}})
	require.NoError(t, err)
	store := configtest.NewStore()
	directory := rte.NewDirectory(catalog, store)
	require.NoError(t, directory.Patch(ctx, "settings", map[string]any{
		"proxy":    "socks5://private-user:private-password@[::1]:1080",
		"rpc_url":  "https://private-user:private-password@example.test:6800/jsonrpc?key=private-key#private-fragment",
		"api_hash": "private-hash", "allowed_users": []string{"123456789", "987654321"},
	}))
	entries := directory.Configurations(ctx)
	require.Equal(t, "socks5://[::1]:1080", entries[0].Previews["proxy"])
	require.Equal(t, "https://example.test:6800/jsonrpc", entries[0].Previews["rpc_url"])
	require.NotContains(t, entries[0].Values, "api_hash")
	encoded, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
	require.Contains(t, string(encoded), `"allowed_users":["123456789","987654321"]`)
}
