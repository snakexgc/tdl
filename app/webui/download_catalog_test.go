package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestCatalogDoesNotRepublishRejectedRemoteCompletion(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{"path": filepath.Join(t.TempDir(), "state")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	require.NoError(t, store.Set(ctx, downloadTaskKeyPrefix+testDocumentID, []byte(`{"id":"`+testDocumentID+`","file_name":"file.bin","file_size":42}`)))
	repository := taskhub.NewAria2Repository(store, 0)
	record := types.Aria2TaskRecord{GID: "late-complete", TaskID: testDocumentID, Status: "active", CreatedAt: time.Now()}
	require.NoError(t, repository.Add(ctx, record))
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var result any = []any{}
		if request.Method == "aria2.tellStopped" {
			// The control operation lands after the observer's snapshot, before
			// the stale RPC response. The repository must retain this newer state.
			paused := record
			paused.Status = "paused"
			if err := repository.Add(ctx, paused); err != nil {
				t.Error(err)
				return
			}
			result = []types.Aria2DownloadStatus{{GID: record.GID, Status: "complete", TotalLength: "42", CompletedLength: "42"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "test", "result": result})
	}))
	defer remote.Close()
	cfg := config.DefaultConfig()
	cfg.Downloader.Mode, cfg.Aria2.RPCURL = config.DownloaderModeAria2, remote.URL
	server := NewServer(Options{Context: config.WithSource(ctx, config.NewSource(cfg)), KVEngine: engine, Namespace: testQueueDefault, NamespaceKV: store})
	items, statusError, err := server.listDownloadLinks(ctx)
	require.NoError(t, err)
	require.Empty(t, statusError)
	require.Len(t, items, 1)
	require.Len(t, items[0].Aria2, 1)
	require.Equal(t, "paused", items[0].Aria2[0].Status)
	require.False(t, items[0].Downloaded)
	data, err := store.Get(ctx, downloadTaskKeyPrefix+testDocumentID)
	require.NoError(t, err)
	var link types.PersistentLink
	require.NoError(t, json.Unmarshal(data, &link))
	require.False(t, link.Downloaded, "rendering must not bypass the observer's revision check")
}

func TestCatalogRemoteSubmissionUsesTypedClientAndPersistsOnce(t *testing.T) {
	initWebUITestConfig(t)
	var additions atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		if request.Method == aria2AddURIMethod {
			additions.Add(1)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"test","result":"catalog-gid"}`))
			return
		}
		require.Equal(t, "aria2.changeGlobalOption", request.Method)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"test","result":"OK"}`))
	}))
	defer remote.Close()
	cfg := config.Get()
	oldMode, oldRPC := cfg.Downloader.Mode, cfg.Aria2.RPCURL
	t.Cleanup(func() { cfg.Downloader.Mode = oldMode; cfg.Aria2.RPCURL = oldRPC })
	cfg.Downloader.Mode = config.DownloaderModeAria2
	cfg.Aria2.RPCURL = remote.URL
	engine := &fakeWebUIKVEngine{meta: kv.Meta{testQueueDefault: {downloadTaskKeyPrefix + testDocumentID: []byte(`{"file_name":"file.bin"}`)}}}
	store, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	server := NewServer(Options{KVEngine: engine, Namespace: testQueueDefault, NamespaceKV: store})
	result := server.downloadLinks(context.Background(), []string{testDocumentID, testDocumentID})
	require.True(t, result.OK, "%v", result.Errors)
	require.Equal(t, 1, result.Added)
	require.Equal(t, 1, result.Skipped)
	require.EqualValues(t, 1, additions.Load())
	data, err := store.Get(context.Background(), aria2TaskKeyPrefix+"catalog-gid")
	require.NoError(t, err)
	var record aria2TaskRecord
	require.NoError(t, json.Unmarshal(data, &record))
	require.Equal(t, testDocumentID, record.TaskID)
	require.Equal(t, testFileName, record.Out)
}
