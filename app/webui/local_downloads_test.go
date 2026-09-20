package webui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/application"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

const testQueueDefault = "default"

func savedLinksForTest(t *testing.T, store storage.Storage, cfg *config.Config) ports.DownloadExecutor {
	t.Helper()
	registry, err := application.Registry()
	require.NoError(t, err)
	host, err := registry.Build(types.DefaultAccount, map[string]bool{ports.NamingRulesName: true}, map[string]map[string]any{ports.NamingRulesName: {"filename": cfg.Filename, "directory": cfg.DownloadDir, "max_bytes": cfg.FilenameMax}})
	require.NoError(t, err)
	host.Start(context.Background())
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.NamingRulesName)
	require.NoError(t, err)
	return local.SavedLinks{Account: types.DefaultAccount, Root: t.TempDir(), Naming: value.(ports.NamingRules), Source: httpdl.NewTaskStore(store, 0), Repository: taskhub.NewLocalRepository(store)}
}

func TestDownloadLinksUsesLocalDownloaderMode(t *testing.T) {
	initWebUITestConfig(t)

	cfg := config.Get()
	oldMode := cfg.Downloader.Executors
	oldDir := cfg.Aria2.Dir
	oldDownloadDir := cfg.DownloadDir
	defer func() {
		cfg.Downloader.Executors = oldMode
		cfg.Aria2.Dir = oldDir
		cfg.DownloadDir = oldDownloadDir
	}()
	cfg.Downloader.Executors = []string{config.DownloadExecutorLocal}
	cfg.Aria2.Dir = t.TempDir()
	cfg.DownloadDir = "P"

	createdAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	taskData := []byte(`{
		"id":"document_42",
		"peer_id":12345,
		"message_id":7,
		"peer":{"kind":"channel","id":12345,"access_hash":99},
		"file_name":"video.mp4",
		"file_size":100,
		"media":{
			"name":"video.mp4",
			"size":100,
			"dc":2,
			"date":0,
			"location":{"kind":"document","id":42,"access_hash":99,"file_reference":"cmVm"}
		},
		"created_at":"` + createdAt.Format(time.RFC3339Nano) + `",
        "last_active_at":"` + createdAt.Format(time.RFC3339Nano) + `"
	}`)

	engine := &fakeWebUIKVEngine{meta: map[string]map[string][]byte{
		testQueueDefault: {
			downloadTaskKeyPrefix + "document_42": taskData,
		},
	}}
	namespaceKV, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	indexCatalogFixtures(t, engine)
	server := NewServer(Options{KVEngine: engine, Namespace: testQueueDefault, NamespaceKV: namespaceKV, LocalLinks: savedLinksForTest(t, namespaceKV, cfg)})

	result := server.downloadLinks(context.Background(), []string{"document_42"})
	require.True(t, result.OK)
	require.Equal(t, 1, result.Added)

	items, statusErr, err := server.listDownloadLinks(context.Background())
	require.NoError(t, err)
	require.Empty(t, statusErr)
	require.Len(t, items, 1)
	require.Len(t, items[0].Local, 1)
	require.Equal(t, types.LocalDownloadStatusQueued, items[0].Local[0].Status)
	require.Contains(t, items[0].Local[0].Path, "12345")
}

func TestMarkDownloadTaskDownloadedPreservesLocalDownloadMetadata(t *testing.T) {
	initWebUITestConfig(t)

	cfg := config.Get()
	oldDir := cfg.Aria2.Dir
	oldDownloadDir := cfg.DownloadDir
	defer func() {
		cfg.Aria2.Dir = oldDir
		cfg.DownloadDir = oldDownloadDir
	}()
	cfg.Aria2.Dir = t.TempDir()
	cfg.DownloadDir = "P"

	taskData := []byte(`{
		"id":"document_42",
		"peer_id":12345,
		"message_id":7,
		"peer":{"kind":"channel","id":12345,"access_hash":9007199254740993},
		"file_name":"video.mp4",
		"file_size":100,
		"media":{
			"name":"video.mp4",
			"size":100,
			"dc":2,
			"date":0,
			"location":{"kind":"document","id":42,"access_hash":9007199254740993,"file_reference":"cmVm"}
		},
		"created_at":"2026-05-01T08:00:00Z", "last_active_at":"2026-05-01T08:00:00Z"
	}`)

	engine := &fakeWebUIKVEngine{meta: map[string]map[string][]byte{
		testQueueDefault: {
			downloadTaskKeyPrefix + "document_42": taskData,
		},
	}}
	namespaceKV, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	server := NewServer(Options{KVEngine: engine, Namespace: testQueueDefault, NamespaceKV: namespaceKV})

	server.markDownloadTaskDownloaded(context.Background(), "document_42")

	data, err := namespaceKV.Get(context.Background(), downloadTaskKeyPrefix+"document_42")
	require.NoError(t, err)
	var raw struct {
		Downloaded bool `json:"downloaded"`
		Media      struct {
			Location struct {
				Kind string `json:"kind"`
			} `json:"location"`
		} `json:"media"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	require.True(t, raw.Downloaded)
	require.Equal(t, "document", raw.Media.Location.Kind)

	_, err = savedLinksForTest(t, namespaceKV, cfg).Submit(context.Background(), types.DownloadSubmission{Account: types.DefaultAccount, TaskID: "document_42"})
	require.NoError(t, err)
	items, err := local.NewController(taskhub.NewLocalRepository(namespaceKV)).List(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, types.LocalDownloadStatusQueued, items[0].Status)
}

func TestDownloadLinksUsesCompletedHTTPDeliveryStatus(t *testing.T) {
	initWebUITestConfig(t)

	cfg := config.Get()
	oldMode := cfg.Downloader.Executors
	defer func() { cfg.Downloader.Executors = oldMode }()
	cfg.Downloader.Executors = []string{config.DownloadExecutorLocal}

	completedAt := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	taskData := []byte(`{
		"id":"document_42",
		"peer_id":12345,
		"message_id":7,
		"file_name":"video.mp4",
		"file_size":100,
		"created_at":"2026-08-12T08:00:00Z",
        "last_active_at":"2026-08-12T08:00:00Z",
		"downloaded":false,
		"http_delivery":{"file_size":100,"completed_at":"` + completedAt.Format(time.RFC3339Nano) + `"}
	}`)

	engine := &fakeWebUIKVEngine{meta: map[string]map[string][]byte{
		testQueueDefault: {
			downloadTaskKeyPrefix + "document_42": taskData,
		},
	}}
	namespaceKV, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	indexCatalogFixtures(t, engine)
	server := NewServer(Options{KVEngine: engine, Namespace: testQueueDefault, NamespaceKV: namespaceKV})

	items, statusErr, err := server.listDownloadLinks(context.Background())
	require.NoError(t, err)
	require.Empty(t, statusErr)
	require.Len(t, items, 1)
	require.True(t, items[0].Downloaded)
	require.True(t, items[0].HTTPDownloaded)
	require.NotNil(t, items[0].HTTPDownloadedAt)
	require.Equal(t, completedAt, *items[0].HTTPDownloadedAt)
	require.Equal(t, int64(100), items[0].HTTPDeliveredBytes)

	data, err := namespaceKV.Get(context.Background(), downloadTaskKeyPrefix+"document_42")
	require.NoError(t, err)
	var persisted struct {
		Downloaded bool `json:"downloaded"`
	}
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.True(t, persisted.Downloaded, "KV listing should self-heal the generic downloaded flag")
}
