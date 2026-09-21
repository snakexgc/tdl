package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/services/localfs"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestLocalDownloadPageDoesNotQueryAria2(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Downloader.Executors = []string{"local", "aria2", "http"}
	cfg.Downloader.LocalRoot = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Downloader.LocalRoot, "file.bin"), []byte("downloaded"), 0o600))
	partial := filepath.Join(cfg.Downloader.LocalRoot, "partial.bin")
	require.NoError(t, os.WriteFile(partial, []byte("part"), 0o600))
	engine, err := kv.New(kv.DriverFile, filepath.Join(t.TempDir(), "state"))
	require.NoError(t, err)
	defer engine.Close()
	storage, err := engine.Open(cfg.Namespace)
	require.NoError(t, err)
	require.NoError(t, taskhub.NewLocalRepository(storage).Save(context.Background(), types.LocalDownloadRecord{ID: "partial", Path: partial, Total: 100, Status: types.LocalDownloadStatusPaused}))
	port := &downloadControlStub{}
	s := NewServer(Options{Context: config.WithSource(context.Background(), config.NewSource(cfg)), DownloadControl: port, NamespaceKV: storage})
	response := httptest.NewRecorder()
	s.handleDownloadTasks(response, httptest.NewRequest(http.MethodGet, "/api/download-tasks?executor=aria2", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"items":[]}`, response.Body.String())
	_, err = s.downloadTasksSnapshot("aria2")(context.Background())
	require.NoError(t, err)
	require.Zero(t, port.calls)
	response = httptest.NewRecorder()
	s.handleDownloadStorage(response, httptest.NewRequest(http.MethodGet, "/api/download-storage", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var usage localfs.Usage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &usage))
	require.Empty(t, usage.Errors)
	require.EqualValues(t, 1, usage.FileCount)
	require.EqualValues(t, len("downloaded"), usage.FileBytes)
	require.Equal(t, cfg.Downloader.LocalRoot, usage.Root)
	response = httptest.NewRecorder()
	s.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/download-storage", nil))
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestAria2ModeDoesNotInspectRemoteDirectoryAsLocalStorage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Aria2.Dir = "/remote/downloads"
	s := NewServer(Options{Context: config.WithSource(context.Background(), config.NewSource(cfg))})
	response := httptest.NewRecorder()
	s.handleDownloadStorage(response, httptest.NewRequest(http.MethodGet, "/api/download-storage", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
}
