package rte_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

type persistedComponent struct {
	component
	value int
}

func (p *persistedComponent) Init(_ context.Context, k rte.Kernel) error {
	return k.Config.Get(limitField, &p.value)
}

func (p *persistedComponent) PrepareConfig(_ context.Context, view config.View) (func(), error) {
	var next int
	if err := view.Get(limitField, &next); err != nil {
		return nil, err
	}
	return func() { p.value = next }, nil
}

func TestPersistedConfigurationFailureAndRestart(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	var current *persistedComponent
	minimum := int64(1)
	schema := []manifest.ConfigField{{Name: limitField, Type: manifest.Int, Default: 1, Min: &minimum}}
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Config: schema}, func() rte.Component {
		current = &persistedComponent{}
		return current
	}))
	repository := &configtest.Repository{}
	store := config.NewManaged(repository)
	host, err := registry.BuildStored(ctx, types.DefaultAccount, store)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	require.Equal(t, 1, current.value)
	require.NoError(t, host.ReconfigureSaved(ctx, providerID, map[string]any{limitField: 2}, store))
	require.Equal(t, 2, current.value)
	require.Error(t, host.ReconfigureSaved(ctx, providerID, map[string]any{limitField: 0}, store))
	repository.SaveError = errors.New("storage unavailable")
	require.Error(t, host.ReconfigureSaved(ctx, providerID, map[string]any{limitField: 3}, store))
	repository.SaveError = nil
	require.Equal(t, 2, current.value)
	require.NoError(t, host.Stop(ctx))
	// Equal values must not bypass lifecycle checks and overwrite a disabled
	// document with enabled=true after shutdown.
	savedView, err := config.New(schema, map[string]any{limitField: 2})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, providerID, false, savedView))
	require.Error(t, host.ReconfigureSaved(ctx, providerID, map[string]any{limitField: 2}, store))
	require.Error(t, host.Reconfigure(ctx, providerID, map[string]any{limitField: 2}))
	document, err := store.Load(ctx, providerID)
	require.NoError(t, err)
	require.False(t, document.Enabled)
	require.NoError(t, store.Save(ctx, providerID, true, savedView))
	restarted, err := registry.BuildStored(ctx, types.DefaultAccount, store)
	require.NoError(t, err)
	require.Equal(t, rte.Running, restarted.Start(ctx)[0].State)
	require.Equal(t, 2, current.value)
	require.NoError(t, restarted.Stop(ctx))
	view, err := config.New(schema, nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, providerID, false, view))
	disabled, err := registry.BuildStored(ctx, types.DefaultAccount, store)
	require.NoError(t, err)
	require.Empty(t, disabled.Start(ctx))
	require.NoError(t, disabled.Stop(ctx))
}
