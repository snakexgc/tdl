package panel

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type testConfigurationStore struct {
	value *types.RuntimeConfig
	saves int
}

func (s *testConfigurationStore) Read(context.Context) (*types.RuntimeConfig, error) {
	return cloneConfiguration(s.value)
}
func (*testConfigurationStore) Validate(*types.RuntimeConfig) error { return nil }
func (s *testConfigurationStore) Save(_ context.Context, before, next *types.RuntimeConfig) error {
	if !reflect.DeepEqual(s.value, before) {
		return ports.ErrConfigurationConflict
	}
	s.value = next
	s.saves++
	return nil
}

func TestConfigurationPortProtectsOwnedFieldsAndSecrets(t *testing.T) {
	ctx := context.Background()
	store := &testConfigurationStore{value: &types.RuntimeConfig{Namespace: "default", WebUI: types.WebUIConfig{Username: "admin", Password: "private"}, Bot: types.BotConfig{Token: "secret"}}}
	service := NewConfiguration(store, func() bool { return true })
	for path, raw := range map[string]string{"FileSizeMinMB": "9", ".namespace": `"other"`, "telegram": `{"api_id":7}`, "unknown": "1", "webui": `{"username":""}`} {
		_, err := service.Patch(ctx, map[string]json.RawMessage{path: json.RawMessage(raw)})
		require.Error(t, err, path)
	}
	require.Zero(t, store.saves)
	result, err := service.Patch(ctx, map[string]json.RawMessage{"bot.token": json.RawMessage(`""`), "limit": json.RawMessage("4")})
	require.NoError(t, err)
	require.Equal(t, 4, result.Limit)
	require.Empty(t, result.Bot.Token)
	require.Empty(t, result.WebUI.Password)
	require.Equal(t, "secret", store.value.Bot.Token)
	require.Equal(t, "private", store.value.WebUI.Password)
	result.Limit = 88
	require.Equal(t, 4, store.value.Limit)
}
