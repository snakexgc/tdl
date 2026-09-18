package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type downloadControlStub struct {
	request types.DownloadAction
	calls   int
}

func (d *downloadControlStub) Tasks(_ context.Context, account types.AccountID, executor string) ([]types.DownloadTask, error) {
	d.calls++
	return []types.DownloadTask{{Account: account, Executor: executor, ID: "example", Status: "paused"}}, nil
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
