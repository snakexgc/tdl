package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/services/logging"
	"github.com/snakexgc/tdl/pkg/config"
)

const testLogAccount = "alice"

func TestLogsFilterAccountsModulesPaginationAndExport(t *testing.T) {
	store := logging.New(nil)
	now := time.Now()
	for _, entry := range []logging.Entry{
		{Account: testLogAccount, Component: logLocalComponent, Message: "local started", Level: logInfoLevel, At: now.Add(-time.Hour * 48)},
		{Account: testLogAccount, Component: logLocalComponent, Message: "local failed", Level: fieldError, Kind: logDiagnosticKind, At: now},
		{Account: testLogAccount, Component: "host.aria2", Message: "aria2 ready", Level: logInfoLevel, At: now},
		{Account: testLogAccount, Component: logForwardComponent, Message: "forward completed", Level: logInfoLevel, At: now},
		{Account: "bob", Component: logLocalComponent, Message: "other account private log", Level: logInfoLevel, At: now},
		{Component: logSystemComponent, Message: "shared system log", Level: "warn", At: now},
	} {
		require.NoError(t, store.Write(entry))
	}
	cfg := config.DefaultConfig()
	cfg.Namespace = testLogAccount
	server := NewServer(Options{Context: logging.WithStore(config.WithSource(context.Background(), config.NewSource(cfg)), store), Namespace: testLogAccount})
	get := func(query string) ([]logItem, int, bool) {
		t.Helper()
		response := httptest.NewRecorder()
		server.handleLogs(response, httptest.NewRequest(http.MethodGet, "/api/logs"+query, nil))
		require.Equal(t, 200, response.Code, response.Body.String())
		var result struct {
			Items []logItem `json:"items"`
			Total int       `json:"total"`
			More  bool      `json:"has_more"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		return result.Items, result.Total, result.More
	}
	items, total, more := get("?limit=2")
	require.Len(t, items, 2)
	require.Equal(t, 5, total)
	require.True(t, more)
	require.Equal(t, "shared system log", items[0].Message)
	items, total, _ = get("?feature=download")
	require.Len(t, items, 3)
	require.Equal(t, 3, total)
	require.Equal(t, "downloader.aria2", items[0].Component)
	items, _, _ = get("?component=downloader.local&level=error&kind=diagnostic&q=failed")
	require.Len(t, items, 1)
	items, _, _ = get("?before=4&limit=1")
	require.Len(t, items, 1)
	require.Equal(t, uint64(3), items[0].ID)
	items, total, _ = get("?since=" + now.Add(-time.Minute).UTC().Format(time.RFC3339))
	require.Len(t, items, 4)
	require.Equal(t, 4, total)
	response := httptest.NewRecorder()
	server.handleLogs(response, httptest.NewRequest(http.MethodGet, "/api/logs?download=1&feature=download", nil))
	require.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
	require.NotContains(t, response.Body.String(), "other account private log")
	require.NotContains(t, response.Body.String(), "forward completed")
	require.Contains(t, response.Body.String(), "local started")
	for _, query := range []string{"?limit=0", "?limit=501", "?before=-1", "?level=invalid", "?kind=invalid", "?since=not-a-date"} {
		response = httptest.NewRecorder()
		server.handleLogs(response, httptest.NewRequest(http.MethodGet, "/api/logs"+query, nil))
		require.Equal(t, 400, response.Code, query)
	}
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/logs?download=1", nil))
	require.Equal(t, 401, response.Code)
	response = httptest.NewRecorder()
	server.handleLogs(response, httptest.NewRequest(http.MethodPost, "/api/logs", nil))
	require.Equal(t, 405, response.Code)
}
