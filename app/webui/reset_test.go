package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/app/reset"
)

const (
	resetEndpoint     = "/api/system/reset"
	resetConfirmation = `{"confirmation":"RESET_TDL"}`
)

func resetRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, resetEndpoint, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestResetRequiresExplicitConfirmationAndCannotChoosePaths(t *testing.T) {
	calls := 0
	s := NewServer(Options{ResetPlan: reset.New(t.TempDir(), ""), RequestReset: func() { calls++ }})
	for _, body := range []string{"", "{}", `{"confirmation":"wrong"}`, `{"confirmation":"RESET_TDL","path":"/"}`, resetConfirmation + ` {}`} {
		response := httptest.NewRecorder()
		s.handleReset(response, resetRequest(body))
		require.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	response := httptest.NewRecorder()
	request := resetRequest(resetConfirmation)
	request.Header.Set("Content-Type", "text/plain")
	s.handleReset(response, request)
	require.Equal(t, http.StatusUnsupportedMediaType, response.Code)
	require.Zero(t, calls)
	require.False(t, s.shutdownRequested.Load())
	response = httptest.NewRecorder()
	s.handleReset(response, httptest.NewRequest(http.MethodGet, resetEndpoint, nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "targets")
	require.Zero(t, calls)
}

func TestResetFlushesAcceptanceAndDefersDeletionUntilAfterShutdown(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(file, []byte("fixture"), 0o600))
	response := httptest.NewRecorder()
	calls := 0
	s := NewServer(Options{ResetPlan: reset.New(home, ""), RequestReset: func() {
		calls++
		require.True(t, response.Flushed)
		require.Equal(t, http.StatusAccepted, response.Code)
		require.FileExists(t, file, "callback schedules shutdown; handler must not delete live files")
	}})
	s.handleReset(response, resetRequest(resetConfirmation))
	require.Equal(t, 1, calls)
	for _, handler := range []http.HandlerFunc{s.handleReset, s.handleReboot} {
		s.opts.RequestReboot = func() { t.Fatal("reboot cannot supersede reset") }
		duplicate := httptest.NewRecorder()
		handler(duplicate, resetRequest(resetConfirmation))
		require.Equal(t, http.StatusConflict, duplicate.Code)
	}
	require.Equal(t, 1, calls)
	require.NoError(t, s.opts.ResetPlan.Execute())
	require.NoFileExists(t, file)
}

func TestResetRouteRequiresLoginAndRefusesUnavailableMode(t *testing.T) {
	s := NewServer(Options{})
	response := httptest.NewRecorder()
	s.routes().ServeHTTP(response, resetRequest(resetConfirmation))
	require.Equal(t, http.StatusUnauthorized, response.Code)
	response = httptest.NewRecorder()
	s.handleReset(response, resetRequest(resetConfirmation))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.False(t, s.shutdownRequested.Load())
	response = httptest.NewRecorder()
	s.handleReset(response, httptest.NewRequest(http.MethodDelete, resetEndpoint, nil))
	require.Equal(t, http.StatusMethodNotAllowed, response.Code)
}
