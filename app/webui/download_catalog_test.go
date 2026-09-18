package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
)

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
