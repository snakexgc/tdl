package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/kv"
)

var webUITestConfigOnce sync.Once

func initWebUITestConfig(t *testing.T) {
	t.Helper()
	var initErr error
	webUITestConfigOnce.Do(func() {
		initErr = config.Init(t.TempDir())
	})
	require.NoError(t, initErr)

	cfg := config.Get()
	cfg.HTTP.PublicBaseURL = "http://127.0.0.1:22334"
	cfg.HTTP.DownloadLinkTTLHours = 24
	cfg.Aria2.RPCURL = ""
}

func TestListDownloadLinksSkipsDownloadIndexKey(t *testing.T) {
	initWebUITestConfig(t)

	createdAt := time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC)
	taskData, err := json.Marshal(persistentDownloadTask{
		ID:        "document_1",
		FileName:  "file.bin",
		FileSize:  123,
		CreatedAt: createdAt,
	})
	require.NoError(t, err)
	indexData, err := json.Marshal(map[string]time.Time{
		"document_1": createdAt,
	})
	require.NoError(t, err)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		"default": {
			downloadTaskIndexKey:                 indexData,
			downloadTaskKeyPrefix + "document_1": taskData,
		},
	}}
	server := NewServer(Options{KVEngine: engine, Namespace: "default"})

	items, statusErr, err := server.listDownloadLinks(context.Background())
	require.NoError(t, err)
	require.Empty(t, statusErr)
	require.Len(t, items, 1)
	require.Equal(t, "document_1", items[0].ID)
	require.Equal(t, "http://127.0.0.1:22334/download/document_1", items[0].URL)
	require.Equal(t, createdAt, items[0].CreatedAt)
}

func TestDeleteDownloadLinkRefusesDownloadIndexKey(t *testing.T) {
	initWebUITestConfig(t)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		"default": {
			downloadTaskIndexKey: []byte("{}"),
		},
	}}
	namespaceKV, err := engine.Open("default")
	require.NoError(t, err)
	server := NewServer(Options{
		KVEngine:    engine,
		Namespace:   "default",
		NamespaceKV: namespaceKV,
	})

	deleted, err := server.deleteDownloadLink(context.Background(), "index")
	require.Error(t, err)
	require.Zero(t, deleted)
	require.Contains(t, engine.meta["default"], downloadTaskIndexKey)
}

func TestAddAria2URISubmitsSingleHTTPConnection(t *testing.T) {
	var reqBody struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":"gid-1"}`))
	}))
	defer srv.Close()

	gid, err := addAria2URI(context.Background(), config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	}, "http://127.0.0.1:22334/download/document_1", "video.mp4")
	require.NoError(t, err)
	require.Equal(t, "gid-1", gid)
	require.Equal(t, "aria2.addUri", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, []any{"http://127.0.0.1:22334/download/document_1"}, reqBody.Params[0])
	require.Equal(t, map[string]any{
		"out":                       "video.mp4",
		"split":                     "1",
		"max-connection-per-server": "1",
		"continue":                  "true",
		"allow-piece-length-change": "true",
		"allow-overwrite":           "true",
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-webui-aria2",
	}, reqBody.Params[1])
}

func TestRewriteAria2ProxyRequestNormalizesTDLAddURI(t *testing.T) {
	body := []byte(`{
		"jsonrpc":"2.0",
		"id":"retry",
		"method":"aria2.addUri",
		"params":[
			["http://127.0.0.1:22334/download/document_1"],
			{"split":"8","max-connection-per-server":"8","min-split-size":"1M","out":"video.mp4"}
		]
	}`)

	next, err := rewriteAria2ProxyRequest(body, "http://127.0.0.1:22334", "")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	params := decoded["params"].([]any)
	options := params[1].(map[string]any)
	require.Equal(t, "1", options["split"])
	require.Equal(t, "1", options["max-connection-per-server"])
	require.NotContains(t, options, "min-split-size")
	require.Equal(t, "video.mp4", options["out"])
}

func TestRewriteAria2ProxyRequestLeavesExternalAddURI(t *testing.T) {
	body := []byte(`{
		"jsonrpc":"2.0",
		"id":"external",
		"method":"aria2.addUri",
		"params":[
			["http://example.com/download/file.bin"],
			{"split":"8","max-connection-per-server":"8","min-split-size":"1M"}
		]
	}`)

	next, err := rewriteAria2ProxyRequest(body, "http://127.0.0.1:22334", "")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	params := decoded["params"].([]any)
	options := params[1].(map[string]any)
	require.Equal(t, "8", options["split"])
	require.Equal(t, "8", options["max-connection-per-server"])
	require.Equal(t, "1M", options["min-split-size"])
}

func TestInjectAria2SecretAddsTokenToMulticallInnerMethods(t *testing.T) {
	body := []byte(`{
		"jsonrpc":"2.0",
		"id":"retry",
		"method":"system.multicall",
		"params":[[
			{"methodName":"aria2.tellStatus","params":["gid-1"]},
			{"methodName":"aria2.getOption","params":["gid-1"]},
			{"methodName":"system.listMethods","params":[]}
		]]
	}`)

	next, err := injectAria2Secret(body, "secret")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	params := decoded["params"].([]any)
	require.Len(t, params, 1)

	calls := params[0].([]any)
	require.Len(t, calls, 3)

	tellStatus := calls[0].(map[string]any)
	require.Equal(t, "aria2.tellStatus", tellStatus["methodName"])
	require.Equal(t, []any{"token:secret", "gid-1"}, tellStatus["params"])

	getOption := calls[1].(map[string]any)
	require.Equal(t, "aria2.getOption", getOption["methodName"])
	require.Equal(t, []any{"token:secret", "gid-1"}, getOption["params"])

	systemCall := calls[2].(map[string]any)
	require.Equal(t, "system.listMethods", systemCall["methodName"])
	require.Empty(t, systemCall["params"])
}

func TestInjectAria2SecretDoesNotAddTokenToSystemMethod(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"methods","method":"system.listMethods"}`)

	next, err := injectAria2Secret(body, "secret")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	_, ok := decoded["params"]
	require.False(t, ok)
}

func TestInjectAria2SecretDoesNotDuplicateExistingToken(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"status","method":"aria2.tellStatus","params":["token:secret","gid-1"]}`)

	next, err := injectAria2Secret(body, "secret")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	require.Equal(t, []any{"token:secret", "gid-1"}, decoded["params"])
}

type fakeWebUIKVEngine struct {
	meta kv.Meta
}

func (f *fakeWebUIKVEngine) Name() string {
	return "fake"
}

func (f *fakeWebUIKVEngine) MigrateTo() (kv.Meta, error) {
	out := make(kv.Meta, len(f.meta))
	for ns, pairs := range f.meta {
		out[ns] = make(map[string][]byte, len(pairs))
		for key, value := range pairs {
			out[ns][key] = append([]byte(nil), value...)
		}
	}
	return out, nil
}

func (f *fakeWebUIKVEngine) MigrateFrom(meta kv.Meta) error {
	f.meta = meta
	return nil
}

func (f *fakeWebUIKVEngine) Namespaces() ([]string, error) {
	out := make([]string, 0, len(f.meta))
	for ns := range f.meta {
		out = append(out, ns)
	}
	return out, nil
}

func (f *fakeWebUIKVEngine) Open(ns string) (storage.Storage, error) {
	if f.meta == nil {
		f.meta = kv.Meta{}
	}
	if _, ok := f.meta[ns]; !ok {
		f.meta[ns] = map[string][]byte{}
	}
	return &fakeWebUINamespaceKV{engine: f, namespace: ns}, nil
}

func (f *fakeWebUIKVEngine) Close() error {
	return nil
}

var _ io.Closer = (*fakeWebUIKVEngine)(nil)

type fakeWebUINamespaceKV struct {
	engine    *fakeWebUIKVEngine
	namespace string
}

func (f *fakeWebUINamespaceKV) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := f.engine.meta[f.namespace][key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (f *fakeWebUINamespaceKV) Set(_ context.Context, key string, value []byte) error {
	f.engine.meta[f.namespace][key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeWebUINamespaceKV) Delete(_ context.Context, key string) error {
	delete(f.engine.meta[f.namespace], key)
	return nil
}
