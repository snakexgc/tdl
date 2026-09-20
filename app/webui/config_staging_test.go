package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestSystemSettingsPersistWithoutApplyingOrRebooting(t *testing.T) {
	initWebUITestConfig(t)
	ctx := context.Background()
	service, err := configuration.Open(ctx, t.TempDir())
	require.NoError(t, err)
	active := config.DefaultConfig()
	source := config.NewSource(active)
	globalBefore := config.Get().Debug
	reboots := 0
	server := NewServer(Options{
		Context: config.WithSource(ctx, source), ConfigurationManager: service,
		RequestReboot: func() { reboots++ },
	})
	request := func(method, body string) map[string]any {
		t.Helper()
		response := httptest.NewRecorder()
		server.handleConfig(response, httptest.NewRequest(method, "/api/config", strings.NewReader(body)))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var result map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		return result
	}
	result := request(http.MethodPatch, `{"values":{"debug":true}}`)
	require.True(t, result["config"].(map[string]any)["debug"].(bool))
	require.False(t, result["active_config"].(map[string]any)["debug"].(bool))
	require.True(t, result["restart_available"].(bool))
	saved, err := service.System(ctx)
	require.NoError(t, err)
	require.True(t, saved.Debug)
	require.False(t, config.From(server.opts.Context).Debug)
	require.Equal(t, globalBefore, config.Get().Debug)
	require.Zero(t, reboots)
	refreshed := request(http.MethodGet, "")
	require.Equal(t, result["config"], refreshed["config"])
	require.Equal(t, result["active_config"], refreshed["active_config"])
	request(http.MethodPatch, `{"values":{"debug":false}}`)
	refreshed = request(http.MethodGet, "")
	require.Equal(t, refreshed["config"], refreshed["active_config"])
	request(http.MethodPatch, `{"values":{"debug":true}}`)
	response := httptest.NewRecorder()
	server.handleReboot(response, httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 1, reboots)
}
