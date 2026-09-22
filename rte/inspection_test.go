package rte_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/configtest"
)

const passwordField = "password"

func TestRegisteredSchemaOwnsBoundsAndDefaults(t *testing.T) {
	registry := rte.NewRegistry()
	minimum := int64(1)
	defaults := []string{"original"}
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Config: []manifest.ConfigField{
		{Name: limitField, Type: manifest.Int, Default: 2, Min: &minimum},
		{Name: "items", Type: manifest.Strings, Default: defaults},
	}}, func() rte.Component { return &component{} }))
	minimum = 100
	defaults[0] = "mutated"
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	entries := host.Configurations()
	require.Equal(t, int64(1), *entries[0].Fields[0].Min)
	require.Equal(t, []any{"original"}, entries[0].Values["items"])
}

func TestConfigurationInspectionAndSecretPatch(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	minimum := int64(1)
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Config: []manifest.ConfigField{
		{Name: limitField, Type: manifest.Int, Default: 2, Min: &minimum},
		{Name: passwordField, Type: manifest.String, Default: "private-value", Secret: true},
	}}, func() rte.Component { return &persistedComponent{} }))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	entries := host.Configurations()
	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-value")
	entries[0].Values[limitField] = 99
	*entries[0].Fields[0].Min = -100
	store := configtest.NewStore()
	require.NoError(t, host.PatchSaved(ctx, providerID, map[string]any{limitField: 3, passwordField: ""}, store))
	document, err := store.Load(ctx, providerID)
	require.NoError(t, err)
	require.Equal(t, "private-value", document.Values[passwordField])
	require.Equal(t, json.Number("3"), document.Values[limitField])
	require.Error(t, host.PatchSaved(ctx, providerID, map[string]any{limitField: 0, passwordField: "replacement"}, store))
	document, err = store.Load(ctx, providerID)
	require.NoError(t, err)
	require.Equal(t, "private-value", document.Values[passwordField])
	require.Equal(t, json.Number("3"), document.Values[limitField])
}
