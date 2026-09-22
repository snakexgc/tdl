package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
)

func TestAria2SubmissionDoesNotRequireManagementProcess(t *testing.T) {
	for _, mode := range []string{config.DownloadExecutorAria2, config.DownloadExecutorLocal} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if request.Method != "aria2.addUri" {
					t.Errorf("unexpected RPC method: %s", request.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": "0123456789abcdef"})
			}))
			defer rpc.Close()
			cfg := config.DefaultConfig()
			cfg.Downloader.Executors = []string{mode, config.DownloadExecutorHTTP}
			cfg.Aria2.RPCURL = rpc.URL
			ctx := config.WithSource(context.Background(), config.NewSource(cfg))
			engine, err := kv.New(kv.DriverFile, filepath.Join(t.TempDir(), "state"))
			require.NoError(t, err)
			defer engine.Close()
			storage, err := engine.Open(cfg.Namespace)
			require.NoError(t, err)
			manager := &Manager{parent: ctx, aria2Mgr: aria2.NewManager(cfg, storage, nil), aria2Process: rte.NewProcess(ctx, types.DefaultAccount, "host.aria2")}
			result, err := (aria2Submission{manager: manager}).Submit(ctx, types.DownloadSubmission{Account: types.DefaultAccount, TaskID: "document_1", DownloadURL: "http://files.test/download/document_1"})
			if mode == config.DownloadExecutorLocal {
				require.ErrorIs(t, err, ports.ErrDownloadNotAccepted)
				require.Zero(t, calls.Load())
			} else {
				require.NoError(t, err)
				require.Equal(t, "0123456789abcdef", result.ID)
				require.EqualValues(t, 1, calls.Load())
			}
			require.False(t, manager.aria2Process.Running())
		})
	}
}
