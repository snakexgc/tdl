package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

type downloadControlStub struct {
	account types.AccountID
	request types.DownloadAction
	calls   int
}

func (d *downloadControlStub) Tasks(_ context.Context, executor string) ([]types.DownloadTask, error) {
	d.calls++
	return []types.DownloadTask{{Account: d.account, Executor: executor, ID: "example", Status: "paused"}}, nil
}

func (d *downloadControlStub) Control(_ context.Context, request types.DownloadAction) (types.DownloadActionResult, error) {
	d.calls++
	d.request = request
	return types.DownloadActionResult{Changed: 1}, nil
}

func TestDownloadControlAPIUsesPortAndRejectsAccountOverride(t *testing.T) {
	initWebUITestConfig(t)
	port := &downloadControlStub{}
	s := NewServer(Options{Namespace: "a", DownloadControl: port})
	r := httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(`{"executor":"local","action":"pause","ids":["example"]}`))
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Zero(t, port.calls)
	r = httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(`{"executor":"local","action":"pause","ids":["example"]}`))
	w = httptest.NewRecorder()
	s.handleDownloadTaskActions(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, types.AccountID("a"), port.request.Account)
	for _, body := range []string{`{"account":"other","executor":"local","action":"delete_all"}`, `{} {}`, `{"unknown":true}`} {
		r = httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(body))
		w = httptest.NewRecorder()
		s.handleDownloadTaskActions(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	require.Equal(t, 1, port.calls)
	r = httptest.NewRequest(http.MethodGet, "/api/internal-downloads", nil)
	w = httptest.NewRecorder()
	s.handleInternalDownloads(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "example")
}

func TestDownloadTaskObservationUsesTheCurrentSessionPort(t *testing.T) {
	port := &downloadControlStub{account: "scoped"}
	s := NewServer(Options{Namespace: "scoped", DownloadControl: port})
	for _, executor := range []string{localDownloadExecutor, config.DownloaderModeAria2} {
		source := s.snapshotSource("download-tasks-" + executor)
		require.NotNil(t, source)
		data, err := source(context.Background())
		require.NoError(t, err)
		items := data.(map[string]any)[fieldItems].([]types.DownloadTask)
		require.Len(t, items, 1)
		require.Equal(t, types.AccountID("scoped"), items[0].Account)
		require.Equal(t, executor, items[0].Executor)
	}
	require.NotNil(t, s.snapshotSource("downloads"), "legacy topic remains supported")
	require.Nil(t, s.snapshotSource("download-tasks-other"))
}
