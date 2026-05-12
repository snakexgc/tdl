package webui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/kv"
)

func TestDownloadLinksUsesInternalDownloaderMode(t *testing.T) {
	initWebUITestConfig(t)

	cfg := config.Get()
	oldMode := cfg.Downloader.Mode
	oldDir := cfg.Aria2.Dir
	oldDownloadDir := cfg.DownloadDir
	defer func() {
		cfg.Downloader.Mode = oldMode
		cfg.Aria2.Dir = oldDir
		cfg.DownloadDir = oldDownloadDir
	}()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.Aria2.Dir = t.TempDir()
	cfg.DownloadDir = "I"

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
		"created_at":"` + createdAt.Format(time.RFC3339Nano) + `"
	}`)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		"default": {
			downloadTaskKeyPrefix + "document_42": taskData,
		},
	}}
	namespaceKV, err := engine.Open("default")
	require.NoError(t, err)
	server := NewServer(Options{KVEngine: engine, Namespace: "default", NamespaceKV: namespaceKV})

	result := server.downloadLinks(context.Background(), []string{"document_42"})
	require.True(t, result.OK)
	require.Equal(t, 1, result.Added)

	items, statusErr, err := server.listDownloadLinks(context.Background())
	require.NoError(t, err)
	require.Empty(t, statusErr)
	require.Len(t, items, 1)
	require.Len(t, items[0].Internal, 1)
	require.Equal(t, watch.InternalDownloadStatusQueued, items[0].Internal[0].Status)
	require.Contains(t, items[0].Internal[0].Path, "12345")
}

func TestMarkDownloadTaskDownloadedPreservesInternalDownloadMetadata(t *testing.T) {
	initWebUITestConfig(t)

	cfg := config.Get()
	oldDir := cfg.Aria2.Dir
	oldDownloadDir := cfg.DownloadDir
	defer func() {
		cfg.Aria2.Dir = oldDir
		cfg.DownloadDir = oldDownloadDir
	}()
	cfg.Aria2.Dir = t.TempDir()
	cfg.DownloadDir = "I"

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
		"created_at":"2026-05-01T08:00:00Z"
	}`)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		"default": {
			downloadTaskKeyPrefix + "document_42": taskData,
		},
	}}
	namespaceKV, err := engine.Open("default")
	require.NoError(t, err)
	server := NewServer(Options{KVEngine: engine, Namespace: "default", NamespaceKV: namespaceKV})

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

	info, err := watch.NewInternalDownloadController(namespaceKV).AddLink(context.Background(), cfg, "document_42")
	require.NoError(t, err)
	require.Equal(t, watch.InternalDownloadStatusQueued, info.Status)
}
