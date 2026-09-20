package rte_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

func TestDirectoryOfflineValidationSecretsAndDisabledPersistence(t *testing.T) {
	ctx := context.Background()
	definition := rte.Definition{Scope: rte.AccountScope, Manifest: manifest.Manifest{ID: "offline", Config: []manifest.ConfigField{
		{Name: directoryValueField, Type: manifest.Int, Default: 3, EmptyPreserves: true},
		{Name: directorySecretField, Type: manifest.String, Default: "", Secret: true},
	}}, Validate: func(_ context.Context, view config.View) error {
		var value int
		if err := view.Get(directoryValueField, &value); err != nil {
			return err
		}
		if value < 2 {
			return errors.New("value must be at least two")
		}
		return nil
	}}
	catalog, err := rte.NewCatalog(definition)
	require.NoError(t, err)
	store := configtest.NewStore()
	view, err := catalog.View(ctx, "offline", map[string]any{directorySecretField: "keep-me"})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "offline", false, view))
	directory := rte.NewDirectory(catalog, store)
	require.NoError(t, directory.Patch(ctx, "offline", map[string]any{directoryValueField: 7, directorySecretField: ""}))
	document, err := store.Load(ctx, "offline")
	require.NoError(t, err)
	require.False(t, document.Enabled)
	require.Equal(t, "keep-me", document.Values[directorySecretField])
	require.Equal(t, json.Number("7"), document.Values[directoryValueField])
	require.NoError(t, directory.Patch(ctx, "offline", map[string]any{directoryValueField: "", directorySecretField: ""}))
	document, err = store.Load(ctx, "offline")
	require.NoError(t, err)
	require.Equal(t, json.Number("7"), document.Values[directoryValueField], "empty input must preserve a saved numeric credential")
	require.Error(t, directory.Patch(ctx, "offline", map[string]any{directoryValueField: 1}))
	require.Error(t, directory.Patch(ctx, "offline", map[string]any{"unknown": 7}))
	require.Error(t, directory.Patch(ctx, "missing", nil))
	entries := directory.Configurations(ctx)
	require.Len(t, entries, 1)
	require.False(t, entries[0].Enabled)
	require.Equal(t, rte.Stopped, entries[0].State)
	encoded, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "keep-me")
	entries[0].Fields[0].Name = "corrupted"
	require.Equal(t, directoryValueField, directory.Configurations(ctx)[0].Fields[0].Name)
}

type directoryComponent struct {
	component
	preparing chan struct{}
	unblock   chan struct{}
}

func (c *directoryComponent) PrepareConfig(ctx context.Context, _ config.View) (func(), error) {
	close(c.preparing)
	select {
	case <-c.unblock:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return func() {}, nil
}

func TestDirectoryHealthDoesNotWaitForConfigurationAndFollowsReplacement(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	m := manifest.Manifest{ID: "live", Config: []manifest.ConfigField{{Name: directoryValueField, Type: manifest.Int, Default: 1}}}
	c := &directoryComponent{preparing: make(chan struct{}), unblock: make(chan struct{})}
	require.NoError(t, registry.Register(m, func() rte.Component { return c }))
	catalog, err := rte.NewCatalog(rte.Definition{Manifest: m, Scope: rte.ConnectionScope})
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, configtest.NewStore())
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	defer func() { require.NoError(t, host.Stop(ctx)) }()
	var current atomic.Pointer[rte.Runtime]
	current.Store(host)
	require.NoError(t, directory.Bind("connection", current.Load))
	require.Error(t, directory.Bind("connection", current.Load))
	done := make(chan error, 1)
	go func() { done <- directory.Patch(ctx, "live", map[string]any{directoryValueField: 2}) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("saving must not prepare or wait for the running component")
	}
	select {
	case <-c.preparing:
		t.Fatal("saving reconfigured a running component")
	default:
	}
	health := make(chan []rte.Health, 1)
	go func() { health <- directory.Health() }()
	select {
	case entries := <-health:
		require.Equal(t, rte.Running, entries[0].Components[0].State)
	case <-time.After(time.Second):
		t.Error("health waited for prepared configuration")
	}
	require.NoError(t, host.Stop(ctx))
	current.Store(nil)
	require.Empty(t, directory.Health())
	require.NoError(t, directory.Patch(ctx, "live", map[string]any{directoryValueField: 3}))
	entries := directory.Configurations(ctx)
	require.Equal(t, rte.Stopped, entries[0].State)
	encoded, err := json.Marshal(entries[0].Values)
	require.NoError(t, err)
	require.JSONEq(t, `{"value":3}`, string(encoded))
}

const directoryValueField = "value"

const directorySecretField = "secret"

func TestDirectoryChecksLivePortOwnership(t *testing.T) {
	ctx := context.Background()
	declared := manifest.Manifest{ID: consumerID, Provides: []manifest.Port{port()}}
	catalog, err := rte.NewCatalog(rte.Definition{Manifest: declared, Scope: rte.AccountScope})
	require.NoError(t, err)
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(manifest.Manifest{ID: consumerID}, factory))
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}, func() rte.Component {
		return &component{init: func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }}
	}))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(ctx)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	directory := rte.NewDirectory(catalog, nil)
	require.NoError(t, directory.Bind("live", func() *rte.Runtime { return host }))
	_, err = directory.ResolveComponentPort(consumerID, "greeting")
	require.Error(t, err, "a catalog declaration must not grant another live component's port")
}

func TestDirectoryRejectsStaleSaveAndPreservesPendingRestartValues(t *testing.T) {
	const endpointField = "endpoint"
	ctx := context.Background()
	m := manifest.Manifest{ID: "pending", Config: []manifest.ConfigField{
		manifest.Text(endpointField, "Endpoint", "old", false, true),
		manifest.Number("frequency", "Frequency", 1, 1, 100, false),
	}}
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(m, func() rte.Component { return &component{} }))
	catalog, err := rte.NewCatalog(rte.Definition{Manifest: m, Scope: rte.ProcessScope})
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, configtest.NewStore())
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(ctx)
	defer func() { require.NoError(t, host.Stop(ctx)) }()
	require.NoError(t, directory.Bind("pending", func() *rte.Runtime { return host }))
	first := directory.Configurations(ctx)[0]
	require.NoError(t, directory.PatchWithRevision(ctx, m.ID, map[string]any{endpointField: "new"}, first.Revision))
	require.ErrorIs(t, directory.PatchWithRevision(ctx, m.ID, map[string]any{endpointField: "stale"}, first.Revision), rte.ErrConfigurationConflict)
	require.NoError(t, directory.Patch(ctx, m.ID, map[string]any{"frequency": 2}))
	pending := directory.Configurations(ctx)[0]
	require.True(t, pending.PendingRestart)
	values, err := json.Marshal(pending.Values)
	require.NoError(t, err)
	require.JSONEq(t, `{"endpoint":"new","frequency":2}`, string(values))
	require.Equal(t, "old", host.Configurations()[0].Values[endpointField])
	require.EqualValues(t, 1, host.Configurations()[0].Values["frequency"])
	require.NoError(t, directory.Patch(ctx, m.ID, map[string]any{endpointField: "old", "frequency": 1}))
	require.False(t, directory.Configurations(ctx)[0].PendingRestart)
}
