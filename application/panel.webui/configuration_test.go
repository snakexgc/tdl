package panel

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

type testConfigurationStore struct {
	value   ports.SystemConfiguration
	saves   int
	failure error
}

func (s *testConfigurationStore) Read(ctx context.Context) (ports.SystemConfiguration, error) {
	return s.value, ctx.Err()
}

func (s *testConfigurationStore) Save(ctx context.Context, before, next ports.SystemConfiguration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if s.value != before {
		return ports.ErrConfigurationConflict
	}
	s.value = next
	s.saves++
	return nil
}

func TestSystemConfigurationRejectsNonSystemFieldsWithoutSaving(t *testing.T) {
	ctx := context.Background()
	store := &testConfigurationStore{value: ports.SystemConfiguration{Namespace: "default"}}
	service := NewConfiguration(store)
	for key, raw := range map[string]string{"FileSizeMinMB": "9", ".namespace": `"other"`, "namespace": `"other"`, "telegram": `{"api_id":7}`, "unknown": "1", "webui": `{"username":""}`, "Debug": "true", " debug ": "true"} {
		_, err := service.Patch(ctx, map[string]json.RawMessage{key: json.RawMessage(raw), debugSetting: json.RawMessage("true")})
		require.Error(t, err, key)
		require.False(t, store.value.Debug)
	}
	for _, raw := range []string{"null", `"true"`, "1", "{}"} {
		_, err := service.Patch(ctx, map[string]json.RawMessage{debugSetting: json.RawMessage(raw)})
		require.Error(t, err, raw)
	}
	require.Zero(t, store.saves)
	result, err := service.Patch(ctx, map[string]json.RawMessage{debugSetting: json.RawMessage("true")})
	require.NoError(t, err)
	require.True(t, result.Debug)
	require.Equal(t, "default", result.Namespace)
	require.Equal(t, 1, store.saves)
	result.Debug = false
	require.True(t, store.value.Debug)
}

func TestSystemConfigurationPreservesStoreOnConflictAndCancellation(t *testing.T) {
	store := &testConfigurationStore{value: ports.SystemConfiguration{Namespace: "default"}, failure: ports.ErrConfigurationConflict}
	service := NewConfiguration(store)
	_, err := service.Patch(context.Background(), map[string]json.RawMessage{debugSetting: json.RawMessage("true")})
	require.ErrorIs(t, err, ports.ErrConfigurationConflict)
	require.False(t, store.value.Debug)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.Patch(ctx, map[string]json.RawMessage{debugSetting: json.RawMessage("true")})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, store.saves)
}
