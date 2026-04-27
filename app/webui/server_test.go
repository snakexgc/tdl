package webui

import (
	"context"
	"encoding/json"
	"io"
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
