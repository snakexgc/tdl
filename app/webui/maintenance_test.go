package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte/configtest"
)

const storageCleanBody = `{"confirmation":"CLEAN_STORAGE","namespace":"Alice"}`

func storageCleanRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/storage/clean", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func maintenanceServer(t *testing.T) *Server {
	t.Helper()
	engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "state"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("Alice")
	require.NoError(t, err)
	require.NoError(t, store.Set(context.Background(), "resume:file", []byte("resume")))
	return NewServer(Options{Namespace: "Alice", KVEngine: engine, NamespaceKV: store})
}

func TestStorageCleanRequiresLoginAndConfirmationForCurrentAccount(t *testing.T) {
	s := maintenanceServer(t)
	routes := s.routes()
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, storageCleanRequest(storageCleanBody))
	require.Equal(t, http.StatusUnauthorized, response.Code)

	for _, body := range []string{"", "{}", `{"confirmation":"wrong","namespace":"Alice"}`, storageCleanBody + " {}", `{"confirmation":"CLEAN_STORAGE","namespace":"Alice","path":"/"}`} {
		response = httptest.NewRecorder()
		s.handleStorageClean(response, storageCleanRequest(body))
		require.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	response = httptest.NewRecorder()
	s.handleStorageClean(response, storageCleanRequest(strings.ReplaceAll(storageCleanBody, "Alice", "Bob")))
	require.Equal(t, http.StatusConflict, response.Code)
	response = httptest.NewRecorder()
	r := storageCleanRequest(storageCleanBody)
	r.Header.Set("Content-Type", "text/plain")
	s.handleStorageClean(response, r)
	require.Equal(t, http.StatusUnsupportedMediaType, response.Code)
	response = httptest.NewRecorder()
	s.handleStorageClean(response, httptest.NewRequest(http.MethodGet, "/api/storage/clean", nil))
	require.Equal(t, http.StatusMethodNotAllowed, response.Code)
	require.Equal(t, "POST", response.Header().Get("Allow"))
	data, err := s.opts.NamespaceKV.Get(context.Background(), "resume:file")
	require.NoError(t, err)
	require.Equal(t, "resume", string(data))
}

func TestStorageCleanPreservesAccountDataAndOtherNamespaces(t *testing.T) {
	ctx := context.Background()
	s := maintenanceServer(t)
	protected := []string{tgauth.SessionKey, tgauth.AppKey, tgauth.FingerprintKey, "peers:42", "state:42", "chan:42", "access_hash:42"}
	for _, key := range protected {
		require.NoError(t, s.opts.NamespaceKV.Set(ctx, key, []byte("protected")))
	}
	require.NoError(t, s.opts.NamespaceKV.Set(ctx, taskhub.LinkPrefix+"link", []byte("{}")))
	other, err := s.opts.KVEngine.Open("Bob")
	require.NoError(t, err)
	require.NoError(t, other.Set(ctx, "resume:file", []byte("other")))
	r := storageCleanRequest(storageCleanBody)
	login := httptest.NewRecorder()
	require.NoError(t, s.issueSession(login, r))
	r.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	s.routes().ServeHTTP(response, r)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		OK        bool     `json:"ok"`
		Namespace string   `json:"namespace"`
		Deleted   int      `json:"deleted"`
		Kept      int      `json:"kept"`
		Errors    []string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.OK)
	require.Equal(t, "Alice", result.Namespace)
	require.Equal(t, 2, result.Deleted)
	require.Equal(t, len(protected), result.Kept)
	require.Empty(t, result.Errors)
	snapshot, err := s.opts.KVEngine.Snapshot(ctx, "Alice")
	require.NoError(t, err)
	require.NotContains(t, snapshot, "resume:file")
	require.NotContains(t, snapshot, taskhub.LinkPrefix+"link")
	for _, key := range protected {
		require.Equal(t, "protected", string(snapshot[key]), key)
	}
	data, err := other.Get(ctx, "resume:file")
	require.NoError(t, err)
	require.Equal(t, "other", string(data))
	require.False(t, s.maintenanceRunning.Load())
}

func TestStorageCleanRejectsUnavailableDisabledAndConcurrentRequests(t *testing.T) {
	s := NewServer(Options{Namespace: "Alice"})
	response := httptest.NewRecorder()
	s.handleStorageClean(response, storageCleanRequest(storageCleanBody))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	s = maintenanceServer(t)
	s.maintenanceRunning.Store(true)
	response = httptest.NewRecorder()
	s.handleStorageClean(response, storageCleanRequest(storageCleanBody))
	require.Equal(t, http.StatusConflict, response.Code)
	s.maintenanceRunning.Store(false)
	s.opts.ComponentStore = configtest.NewStore()
	view, err := s.opts.Catalog.View(context.Background(), ports.KVMaintenanceName, nil)
	require.NoError(t, err)
	require.NoError(t, s.opts.ComponentStore.Save(context.Background(), ports.KVMaintenanceName, false, view))
	response = httptest.NewRecorder()
	s.handleStorageClean(response, storageCleanRequest(storageCleanBody))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	_, err = s.opts.NamespaceKV.Get(context.Background(), "resume:file")
	require.NoError(t, err)
}
