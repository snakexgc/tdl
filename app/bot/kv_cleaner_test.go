package bot

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/storage"
)

func TestCleanCurrentNamespaceKVPreservesLoginAndStateKeys(t *testing.T) {
	ctx := context.Background()
	engine := &fakeKVEngine{
		meta: map[string]map[string][]byte{
			"default": {
				"session":                  []byte("session"),
				"app":                      []byte("desktop"),
				"peers:key:user:1":         []byte("peer"),
				"state:42":                 []byte("state"),
				"chan:42":                  []byte("chan"),
				"access_hash:channel:42:1": []byte("123"),
				"watch.download.index":     []byte("{}"),
				"watch.download.task-id":   []byte("{}"),
				"watch.aria2.index":        []byte("{}"),
				"watch.aria2.task.gid":     []byte("{}"),
				"resume:file":              []byte("{}"),
			},
			"other": {
				"watch.download.index": []byte("{}"),
			},
		},
	}
	namespaceKV := &fakeNamespaceKV{engine: engine, namespace: "default"}

	result, err := cleanCurrentNamespaceKV(ctx, engine, "default", namespaceKV)
	require.NoError(t, err)
	require.Equal(t, 8, result.Kept)
	require.Equal(t, 3, result.Deleted)

	require.Contains(t, engine.meta["default"], "session")
	require.Contains(t, engine.meta["default"], "app")
	require.Contains(t, engine.meta["default"], "peers:key:user:1")
	require.Contains(t, engine.meta["default"], "state:42")
	require.Contains(t, engine.meta["default"], "chan:42")
	require.Contains(t, engine.meta["default"], "access_hash:channel:42:1")
	require.JSONEq(t, "{}", string(engine.meta["default"]["watch.download.index"]))
	require.JSONEq(t, "{}", string(engine.meta["default"]["watch.aria2.index"]))
	require.NotContains(t, engine.meta["default"], "resume:file")
	require.Contains(t, engine.meta["other"], "watch.download.index")
}

type fakeKVEngine struct {
	mu   sync.Mutex
	meta map[string]map[string][]byte
}

func (f *fakeKVEngine) Name() string {
	return "fake"
}

func (f *fakeKVEngine) Namespaces() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.meta))
	for ns := range f.meta {
		out = append(out, ns)
	}
	return out, nil
}

func (f *fakeKVEngine) Open(ns string) (storage.Storage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meta[ns]; !ok {
		f.meta[ns] = map[string][]byte{}
	}
	return &fakeNamespaceKV{engine: f, namespace: ns}, nil
}

func (f *fakeKVEngine) Close() error {
	return nil
}

type fakeNamespaceKV struct {
	engine    *fakeKVEngine
	namespace string
}

func (f *fakeNamespaceKV) Get(ctx context.Context, key string) ([]byte, error) {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	value, ok := f.engine.meta[f.namespace][key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (f *fakeNamespaceKV) Set(ctx context.Context, key string, value []byte) error {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	f.engine.meta[f.namespace][key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeNamespaceKV) Delete(ctx context.Context, key string) error {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	delete(f.engine.meta[f.namespace], key)
	return nil
}

func (f *fakeKVEngine) Snapshot(ctx context.Context, namespace string) (map[string][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := map[string][]byte{}
	for key, value := range f.meta[namespace] {
		result[key] = append([]byte(nil), value...)
	}
	return result, ctx.Err()
}

func (f *fakeNamespaceKV) Update(ctx context.Context, fn func(storage.Storage) error) error {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	values := make(map[string][]byte)
	for key, value := range f.engine.meta[f.namespace] {
		values[key] = append([]byte(nil), value...)
	}
	next := &fakeKVEngine{meta: map[string]map[string][]byte{f.namespace: values}}
	if err := fn(&fakeNamespaceKV{engine: next, namespace: f.namespace}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.engine.meta[f.namespace] = next.meta[f.namespace]
	return nil
}
