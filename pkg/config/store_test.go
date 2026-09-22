package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type testConfigurationPersistence struct {
	value   *Config
	failure error
}

func installTestConfiguration(t *testing.T) *testConfigurationPersistence {
	t.Helper()
	mu.Lock()
	previous, previousPersist := instance, persist
	mu.Unlock()
	t.Cleanup(func() { Install(previous, previousPersist) })
	store := &testConfigurationPersistence{}
	Install(DefaultConfig(), func(ctx context.Context, _, next *Config) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if store.failure != nil {
			return store.failure
		}
		value, err := Clone(next)
		if err != nil {
			return err
		}
		store.value = value
		return nil
	})
	return store
}

func TestConfigurationRequiresInstalledPersistence(t *testing.T) {
	installTestConfiguration(t)
	before := Get()
	Install(before, nil)
	next, err := Clone(before)
	require.NoError(t, err)
	next.Debug = true
	require.Error(t, Set(next))
	require.Same(t, before, Get())
}
