package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte"
)

type componentTestManager struct {
	savedID string
	values  map[string]any
}

func (*componentTestManager) ComponentConfigurations() ([]rte.Configuration, bool) {
	return []rte.Configuration{{ID: "naming.rules", State: rte.Running}}, true
}

func (*componentTestManager) ComponentHealth() []rte.Health {
	return []rte.Health{{Components: []rte.ComponentHealth{{Status: rte.Status{ID: "example", State: rte.Running}}}}}
}

func TestComponentDiagnosticsRequiresSession(t *testing.T) {
	initWebUITestConfig(t)
	server := NewServer(Options{ComponentManager: &componentTestManager{}})
	request := httptest.NewRequest(http.MethodGet, "/api/components/health", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	response = httptest.NewRecorder()
	server.handleComponentHealth(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "example")
}

func (m *componentTestManager) SaveComponentConfiguration(_ context.Context, id string, values map[string]any) error {
	m.savedID, m.values = id, values
	return nil
}

func TestComponentConfigurationAPIRequiresSession(t *testing.T) {
	initWebUITestConfig(t)
	manager := &componentTestManager{}
	server := NewServer(Options{ComponentManager: manager})
	request := httptest.NewRequest(http.MethodPatch, "/api/components", strings.NewReader(`{"id":"naming.rules","values":{"max_bytes":80}}`))
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Empty(t, manager.savedID)
	// The authenticated handler delegates one typed patch to the owner.
	request = httptest.NewRequest(http.MethodPatch, "/api/components", strings.NewReader(`{"id":"naming.rules","values":{"max_bytes":80}}`))
	response = httptest.NewRecorder()
	server.handleComponents(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "naming.rules", manager.savedID)
	require.Equal(t, json.Number("80"), manager.values["max_bytes"])
	manager.savedID = ""
	request = httptest.NewRequest(http.MethodPatch, "/api/components", strings.NewReader(`{"id":"naming.rules"} {}`))
	response = httptest.NewRecorder()
	server.handleComponents(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Empty(t, manager.savedID)
}

func TestRuntimeSnapshotCannotOverrideStoredPolicies(t *testing.T) {
	initWebUITestConfig(t)
	server := NewServer(Options{ComponentManager: &componentTestManager{}})
	request := httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(`{"values":{"filename":"wrong-F"}}`))
	response := httptest.NewRecorder()
	server.handleConfig(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "component configuration page")
}

func TestRuntimeSnapshotCannotOverrideStoredBotPermissions(t *testing.T) {
	initWebUITestConfig(t)
	server := NewServer(Options{ComponentManager: &componentTestManager{}})
	for _, values := range []string{`{"bot.allowed_users":[123]}`, `{"bot":{"allowed_users":[123]}}`, `{"bot . allowed_users":[123]}`} {
		request := httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(`{"values":`+values+`}`))
		response := httptest.NewRecorder()
		server.handleConfig(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.Contains(t, response.Body.String(), "component configuration page")
	}
}

func TestRuntimeSnapshotCannotOverrideStoredAccountAndTriggers(t *testing.T) {
	initWebUITestConfig(t)
	server := NewServer(Options{ComponentManager: &componentTestManager{}})
	for _, values := range []string{`{"telegram.api_id":123}`, `{"telegram":{"api_id":123,"api_hash":"hidden"}}`, `{"trigger_reactions":["👍"]}`, `{"forward":{"trigger_reactions":["👍"]}}`, `{"forward . trigger_reactions":["👍"]}`} {
		request := httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(`{"values":`+values+`}`))
		response := httptest.NewRecorder()
		server.handleConfig(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code, values)
		require.Contains(t, response.Body.String(), "component configuration page")
	}
}
