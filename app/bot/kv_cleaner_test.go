package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/kv"
)

func TestCleanCurrentNamespaceKVPreservesLoginAndStateKeys(t *testing.T) {
	ctx := context.Background()
	engine := &fakeKVEngine{
		meta: kv.Meta{
			"default": {
				"session":                []byte("session"),
				"app":                    []byte("desktop"),
				"peers:key:user:1":       []byte("peer"),
				"state:42":               []byte("state"),
				"chan:42":                []byte("chan"),
				"watch.download.index":   []byte("{}"),
				"watch.download.task-id": []byte("{}"),
				"watch.aria2.index":      []byte("{}"),
				"watch.aria2.task.gid":   []byte("{}"),
				"resume:file":            []byte("{}"),
			},
			"other": {
				"watch.download.index": []byte("{}"),
			},
		},
	}
	namespaceKV := &fakeNamespaceKV{engine: engine, namespace: "default"}

	result, err := cleanCurrentNamespaceKV(ctx, engine, "default", namespaceKV)
	require.NoError(t, err)
	require.Equal(t, 5, result.Kept)
	require.Equal(t, 5, result.Deleted)

	require.Contains(t, engine.meta["default"], "session")
	require.Contains(t, engine.meta["default"], "app")
	require.Contains(t, engine.meta["default"], "peers:key:user:1")
	require.Contains(t, engine.meta["default"], "state:42")
	require.Contains(t, engine.meta["default"], "chan:42")
	require.NotContains(t, engine.meta["default"], "watch.download.index")
	require.NotContains(t, engine.meta["default"], "watch.aria2.index")
	require.NotContains(t, engine.meta["default"], "resume:file")
	require.Contains(t, engine.meta["other"], "watch.download.index")
}

type fakeKVEngine struct {
	meta kv.Meta
}

func (f *fakeKVEngine) Name() string {
	return "fake"
}

func (f *fakeKVEngine) MigrateTo() (kv.Meta, error) {
	out := make(kv.Meta, len(f.meta))
	for ns, pairs := range f.meta {
		out[ns] = make(map[string][]byte, len(pairs))
		for key, value := range pairs {
			out[ns][key] = append([]byte(nil), value...)
		}
	}
	return out, nil
}

func (f *fakeKVEngine) MigrateFrom(meta kv.Meta) error {
	f.meta = meta
	return nil
}

func (f *fakeKVEngine) Namespaces() ([]string, error) {
	out := make([]string, 0, len(f.meta))
	for ns := range f.meta {
		out = append(out, ns)
	}
	return out, nil
}

func (f *fakeKVEngine) Open(ns string) (storage.Storage, error) {
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
	value, ok := f.engine.meta[f.namespace][key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (f *fakeNamespaceKV) Set(ctx context.Context, key string, value []byte) error {
	f.engine.meta[f.namespace][key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeNamespaceKV) Delete(ctx context.Context, key string) error {
	delete(f.engine.meta[f.namespace], key)
	return nil
}
