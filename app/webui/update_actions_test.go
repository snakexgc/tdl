package webui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type updateActionPort struct {
	ports.Updater
	fail bool
}

func (p *updateActionPort) Download(context.Context) (types.UpdatePlan, types.UpdateInfo, error) {
	if p.fail {
		return types.UpdatePlan{}, types.UpdateInfo{}, errors.New("download failed")
	}
	return types.UpdatePlan{SourcePath: "isolated-candidate", Version: "test-version"}, types.UpdateInfo{NeedsUpdate: true, CanUpdate: true, LatestVersion: "test-version"}, nil
}

func TestShutdownActionsFlushResponseBeforeRequestAndRejectDuplicates(t *testing.T) {
	for _, update := range []bool{false, true} {
		name := "reboot"
		if update {
			name = "apply-update"
		}
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			calls := 0
			requestShutdown := func() {
				calls++
				require.True(t, response.Flushed)
				require.Equal(t, http.StatusOK, response.Code)
				require.Contains(t, response.Body.String(), `"ok":true`)
			}
			server := &Server{opts: Options{Updater: &updateActionPort{}, RequestReboot: requestShutdown, RequestUpdate: func(plan types.UpdatePlan) {
				require.Equal(t, "isolated-candidate", plan.SourcePath)
				requestShutdown()
			}}}
			handler := server.handleReboot
			if update {
				handler = server.handleUpdateApply
			}
			handler(response, httptest.NewRequest(http.MethodPost, "/", nil))
			require.Equal(t, 1, calls, "request must be owned by the handler, not a delayed goroutine")
			duplicate := httptest.NewRecorder()
			handler(duplicate, httptest.NewRequest(http.MethodPost, "/", nil))
			require.Equal(t, http.StatusConflict, duplicate.Code)
			require.Equal(t, 1, calls)
		})
	}
}

func TestFailedUpdateAllowsRetryWithoutRequestingShutdown(t *testing.T) {
	port := &updateActionPort{fail: true}
	calls := 0
	server := &Server{opts: Options{Updater: port, RequestUpdate: func(types.UpdatePlan) { calls++ }}}
	response := httptest.NewRecorder()
	server.handleUpdateApply(response, httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, http.StatusBadGateway, response.Code)
	require.Zero(t, calls)
	require.False(t, server.shutdownRequested.Load())
	port.fail = false
	server.handleUpdateApply(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, 1, calls)
}
