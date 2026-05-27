package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/kv"
)

var webUITestConfigOnce sync.Once

const (
	webUITestUsername = "admin"
	webUITestPassword = "secret"

	testDocumentID  = "document_1"
	testUserAlice   = "Alice"
	testUserBob     = "Bob"
	testGID         = "gid-1"
	testTokenSecret = "token:secret"
)

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

func TestRoutesServeFormLoginWithoutBasicChallenge(t *testing.T) {
	initWebUITestConfig(t)
	cfg := config.Get()
	cfg.WebUI.Username = webUITestUsername
	cfg.WebUI.Password = webUITestPassword

	server := NewServer(Options{})
	handler := server.routes()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))
	require.Empty(t, rec.Header().Get("WWW-Authenticate"))

	req = httptest.NewRequest(http.MethodGet, "/login", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `id="webui-login-form"`)
	require.Empty(t, rec.Header().Get("WWW-Authenticate"))
}

func TestRoutesAuthenticateWithWebSessionCookie(t *testing.T) {
	initWebUITestConfig(t)
	cfg := config.Get()
	cfg.WebUI.Username = webUITestUsername
	cfg.WebUI.Password = webUITestPassword

	server := NewServer(Options{})
	handler := server.routes()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+webUITestUsername+`","password":"`+webUITestPassword+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Result().Cookies())

	var sessionCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == webUICookieName {
			sessionCookie = cookie
			break
		}
	}
	require.NotNil(t, sessionCookie)
	require.True(t, sessionCookie.HttpOnly)

	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"namespace"`)

	req = httptest.NewRequest(http.MethodGet, "/api/heartbeat", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"ok":true`)

	req = httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"process"`)
	require.Contains(t, rec.Body.String(), `"download"`)

	req = httptest.NewRequest(http.MethodGet, "/views/user.html", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `id="view-user"`)
}

func TestRoutesServeAppShellForViewPaths(t *testing.T) {
	initWebUITestConfig(t)
	cfg := config.Get()
	cfg.WebUI.Username = webUITestUsername
	cfg.WebUI.Password = webUITestPassword

	handler := NewServer(Options{}).routes()

	// Unauthenticated deep links redirect to the login page like "/" does.
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))

	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+webUITestUsername+`","password":"`+webUITestPassword+`"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	sessionCookie := rec.Result().Cookies()[0]

	// Authenticated view paths render the SPA shell so a refresh stays put.
	for _, path := range []string{"/dashboard", "/config", "/kv/extra/segments"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(sessionCookie)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusOK, rec.Code, "path %s", path)
		require.Containsf(t, rec.Body.String(), `id="view-host"`, "path %s", path)
		require.Containsf(t, rec.Body.String(), `/static/js/main.js`, "path %s", path)
	}

	// Module scripts and stylesheets are embedded from subdirectories and
	// served with a strict MIME type that lets browsers evaluate ES modules.
	req = httptest.NewRequest(http.MethodGet, "/static/js/main.js", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "javascript")

	req = httptest.NewRequest(http.MethodGet, "/static/css/base.css", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/css")
}

func TestConfigAPIExposesSplitListenFields(t *testing.T) {
	initWebUITestConfig(t)
	cfg := config.Get()
	previous, err := cloneConfig(cfg)
	require.NoError(t, err)
	defer func() {
		*cfg = *previous
	}()

	cfg.HTTP.Listen = ""
	cfg.HTTP.Address = "0.0.0.0"
	cfg.HTTP.Port = 22334
	cfg.WebUI.Listen = ""
	cfg.WebUI.Address = "0.0.0.0"
	cfg.WebUI.Port = 22335
	cfg.WebUI.Username = webUITestUsername
	cfg.WebUI.Password = webUITestPassword

	handler := NewServer(Options{}).routes()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+webUITestUsername+`","password":"`+webUITestPassword+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Result().Cookies())

	req = httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	publicCfg, ok := body["config"].(map[string]any)
	require.True(t, ok)
	httpCfg, ok := publicCfg["http"].(map[string]any)
	require.True(t, ok)
	webUICfg, ok := publicCfg["webui"].(map[string]any)
	require.True(t, ok)

	require.Equal(t, "0.0.0.0", httpCfg["address"])
	require.Equal(t, float64(22334), httpCfg["port"])
	require.NotContains(t, httpCfg, "listen")
	require.Equal(t, "0.0.0.0", webUICfg["address"])
	require.Equal(t, float64(22335), webUICfg["port"])
	require.NotContains(t, webUICfg, "listen")
}

func TestRoutesRejectUnauthenticatedAPIWithJSON(t *testing.T) {
	initWebUITestConfig(t)
	cfg := config.Get()
	cfg.WebUI.Username = webUITestUsername
	cfg.WebUI.Password = webUITestPassword

	server := NewServer(Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	server.routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Empty(t, rec.Header().Get("WWW-Authenticate"))
	require.Contains(t, rec.Body.String(), "authentication required")
}

func TestRoutesProtectAria2Entrypoints(t *testing.T) {
	initWebUITestConfig(t)
	cfg := config.Get()
	cfg.WebUI.Username = webUITestUsername
	cfg.WebUI.Password = webUITestPassword

	handler := NewServer(Options{}).routes()

	req := httptest.NewRequest(http.MethodGet, "/aria2ng.html", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))
	require.Empty(t, rec.Header().Get("WWW-Authenticate"))

	req = httptest.NewRequest(http.MethodPost, "/aria2/jsonrpc", strings.NewReader(`{"jsonrpc":"2.0","method":"aria2.tellActive","id":"x"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Empty(t, rec.Header().Get("WWW-Authenticate"))
	require.Contains(t, rec.Body.String(), "authentication required")
}

func TestListDownloadLinksSkipsDownloadIndexKey(t *testing.T) {
	initWebUITestConfig(t)

	createdAt := time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC)
	taskData, err := json.Marshal(persistentDownloadTask{
		ID:        testDocumentID,
		FileName:  "file.bin",
		FileSize:  123,
		CreatedAt: createdAt,
	})
	require.NoError(t, err)
	indexData, err := json.Marshal(map[string]time.Time{
		testDocumentID: createdAt,
	})
	require.NoError(t, err)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		testQueueDefault: {
			downloadTaskIndexKey:                   indexData,
			downloadTaskKeyPrefix + testDocumentID: taskData,
		},
	}}
	server := NewServer(Options{KVEngine: engine, Namespace: testQueueDefault})

	items, statusErr, err := server.listDownloadLinks(context.Background())
	require.NoError(t, err)
	require.Empty(t, statusErr)
	require.Len(t, items, 1)
	require.Equal(t, testDocumentID, items[0].ID)
	require.Equal(t, "http://127.0.0.1:22334/download/"+testDocumentID, items[0].URL)
	require.Equal(t, createdAt, items[0].CreatedAt)
}

func TestListDownloadLinksDiscoversRetriedAria2GIDByDownloadURL(t *testing.T) {
	initWebUITestConfig(t)

	createdAt := time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC)
	taskData, err := json.Marshal(persistentDownloadTask{
		ID:        testDocumentID,
		FileName:  "video.mp4",
		FileSize:  100,
		CreatedAt: createdAt,
	})
	require.NoError(t, err)
	oldRecordData, err := json.Marshal(aria2TaskRecord{
		GID:         "old-gid",
		TaskID:      testDocumentID,
		DownloadURL: "http://127.0.0.1:22334/download/" + testDocumentID,
		CreatedAt:   createdAt,
		Status:      "error",
		Error:       "EOF",
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "aria2.tellStopped":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":[{"gid":"new-gid","status":"complete","totalLength":"100","completedLength":"100","files":[{"length":"100","completedLength":"100","uris":[{"uri":"http://127.0.0.1:22334/download/` + testDocumentID + `"}]}]},{"gid":"foreign-gid","status":"complete","totalLength":"100","completedLength":"100","files":[{"uris":[{"uri":"http://127.0.0.1:22334/download/document_2"}]}]}]}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":[]}`))
		}
	}))
	defer srv.Close()

	cfg := config.Get()
	oldRPC := cfg.Aria2.RPCURL
	oldBase := cfg.HTTP.PublicBaseURL
	cfg.Aria2.RPCURL = srv.URL
	cfg.HTTP.PublicBaseURL = "http://127.0.0.1:22334"
	defer func() {
		cfg.Aria2.RPCURL = oldRPC
		cfg.HTTP.PublicBaseURL = oldBase
	}()

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		testQueueDefault: {
			downloadTaskKeyPrefix + testDocumentID: taskData,
			aria2TaskKeyPrefix + "old-gid":         oldRecordData,
		},
	}}
	namespaceKV, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	server := NewServer(Options{KVEngine: engine, Namespace: testQueueDefault, NamespaceKV: namespaceKV})

	items, statusErr, err := server.listDownloadLinks(context.Background())
	require.NoError(t, err)
	require.Empty(t, statusErr)
	require.Len(t, items, 1)
	require.True(t, items[0].Downloaded)
	require.Len(t, items[0].Aria2, 2)

	var retried aria2LinkEntry
	for _, entry := range items[0].Aria2 {
		if entry.GID == "new-gid" {
			retried = entry
		}
	}
	require.Equal(t, "new-gid", retried.GID)
	require.Equal(t, aria2StatusComplete, retried.Status)
	require.True(t, retried.Downloaded)

	data, err := namespaceKV.Get(context.Background(), aria2TaskKeyPrefix+"new-gid")
	require.NoError(t, err)
	var saved aria2TaskRecord
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, testDocumentID, saved.TaskID)
	require.Equal(t, "http://127.0.0.1:22334/download/"+testDocumentID, saved.DownloadURL)
	_, err = namespaceKV.Get(context.Background(), aria2TaskKeyPrefix+"foreign-gid")
	require.ErrorIs(t, err, storage.ErrNotFound)

	data, err = namespaceKV.Get(context.Background(), downloadTaskKeyPrefix+testDocumentID)
	require.NoError(t, err)
	var task persistentDownloadTask
	require.NoError(t, json.Unmarshal(data, &task))
	require.True(t, task.Downloaded)
}

func TestListUserSessionsOnlyReturnsNamespacesWithSession(t *testing.T) {
	initWebUITestConfig(t)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		testUserAlice: {
			userSessionKey: []byte("alice-session"),
		},
		testUserBob: {
			userSessionKey: []byte("bob-session"),
		},
		"Cache": {
			"watch.download.document_1": []byte("{}"),
		},
		"Bob1": {
			userSessionKey: []byte("invalid-name-session"),
		},
		"Empty": {
			userSessionKey: []byte{},
		},
	}}
	server := NewServer(Options{KVEngine: engine, Namespace: testUserBob})

	sessions, err := server.listUserSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []userSessionOption{
		{Namespace: testUserBob, Current: true},
		{Namespace: testUserAlice},
	}, sessions)
}

func TestDeleteUserSessionRemovesLoginKeysOnly(t *testing.T) {
	initWebUITestConfig(t)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		testUserAlice: {
			userSessionKey:                   []byte("alice-session"),
			userAppKey:                       []byte("desktop"),
			downloadTaskKeyPrefix + "item_1": []byte("{}"),
		},
		testUserBob: {
			userSessionKey: []byte("bob-session"),
		},
	}}
	server := NewServer(Options{KVEngine: engine, Namespace: testUserBob})

	deleted, err := server.deleteUserSession(context.Background(), testUserAlice)
	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	require.NotContains(t, engine.meta[testUserAlice], userSessionKey)
	require.NotContains(t, engine.meta[testUserAlice], userAppKey)
	require.Contains(t, engine.meta[testUserAlice], downloadTaskKeyPrefix+"item_1")

	sessions, err := server.listUserSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []userSessionOption{{Namespace: testUserBob, Current: true}}, sessions)
}

func TestHandleUserDeleteRejectsCurrentUser(t *testing.T) {
	initWebUITestConfig(t)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		testUserBob: {
			userSessionKey: []byte("bob-session"),
		},
	}}
	server := NewServer(Options{KVEngine: engine, Namespace: testUserBob})
	req := httptest.NewRequest(http.MethodPost, "/api/user/delete", strings.NewReader(`{"namespace":"`+testUserBob+`"}`))
	rec := httptest.NewRecorder()

	server.handleUserDelete(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, engine.meta[testUserBob], userSessionKey)
}

func TestDeleteDownloadLinkRefusesDownloadIndexKey(t *testing.T) {
	initWebUITestConfig(t)

	engine := &fakeWebUIKVEngine{meta: kv.Meta{
		testQueueDefault: {
			downloadTaskIndexKey: []byte("{}"),
		},
	}}
	namespaceKV, err := engine.Open(testQueueDefault)
	require.NoError(t, err)
	server := NewServer(Options{
		KVEngine:    engine,
		Namespace:   testQueueDefault,
		NamespaceKV: namespaceKV,
	})

	deleted, err := server.deleteDownloadLink(context.Background(), "index")
	require.Error(t, err)
	require.Zero(t, deleted)
	require.Contains(t, engine.meta[testQueueDefault], downloadTaskIndexKey)
}

func TestAddAria2URISubmitsSingleHTTPConnection(t *testing.T) {
	var reqBody struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":"` + testGID + `"}`))
	}))
	defer srv.Close()

	gid, err := addAria2URI(context.Background(), config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	}, "http://127.0.0.1:22334/download/"+testDocumentID, "video.mp4", 1)
	require.NoError(t, err)
	require.Equal(t, testGID, gid)
	require.Equal(t, "aria2.addUri", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, []any{"http://127.0.0.1:22334/download/" + testDocumentID}, reqBody.Params[0])
	require.Equal(t, map[string]any{
		"out":                       "video.mp4",
		"split":                     "1",
		"max-connection-per-server": "1",
		"min-split-size":            "1024K",
		"piece-length":              "1024K",
		"timeout":                   "600",
		"continue":                  valueTrue,
		"allow-piece-length-change": valueTrue,
		"allow-overwrite":           valueTrue,
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-webui-aria2",
	}, reqBody.Params[1])
}

func TestAddAria2URISubmitsClientRangeConnections(t *testing.T) {
	var reqBody struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":"` + testGID + `"}`))
	}))
	defer srv.Close()

	_, err := addAria2URI(context.Background(), config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	}, "http://127.0.0.1:22334/download/"+testDocumentID, "video.mp4", 4)
	require.NoError(t, err)
	options := reqBody.Params[1].(map[string]any)
	require.Equal(t, "4", options["split"])
	require.Equal(t, "4", options["max-connection-per-server"])
	require.Equal(t, "1024K", options["min-split-size"])
	require.Equal(t, "1024K", options["piece-length"])
	require.Equal(t, "600", options["timeout"])
}

func TestConfigureAria2MaxConcurrentDownloads(t *testing.T) {
	var reqBody struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":"OK"}`))
	}))
	defer srv.Close()

	err := configureAria2MaxConcurrentDownloads(context.Background(), config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	}, 3)
	require.NoError(t, err)
	require.Equal(t, "aria2.changeGlobalOption", reqBody.Method)
	require.Equal(t, []any{map[string]any{"max-concurrent-downloads": "3"}}, reqBody.Params)
}

func TestCheckAria2RequiresRPCURL(t *testing.T) {
	result := checkAria2(context.Background(), config.Aria2Config{})

	require.False(t, result.OK)
	require.False(t, result.Configured)
	require.Contains(t, result.Message, "aria2.rpc_url")
}

func TestCheckAria2Success(t *testing.T) {
	var reqBody struct {
		Method string `json:"method"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-webui","result":{"version":"1.37.0"}}`))
	}))
	defer srv.Close()

	result := checkAria2(context.Background(), config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	})

	require.True(t, result.OK)
	require.True(t, result.Configured)
	require.Equal(t, "aria2.getVersion", reqBody.Method)
	require.Equal(t, "1.37.0", result.Version)
	require.Contains(t, result.Message, "连接正常")
}

func TestRewriteAria2ProxyRequestNormalizesTDLAddURI(t *testing.T) {
	body := []byte(`{
		"jsonrpc":"2.0",
		"id":"retry",
		"method":"aria2.addUri",
		"params":[
			["http://127.0.0.1:22334/download/document_1"],
			{"split":"8","max-connection-per-server":"8","min-split-size":"2M","piece-length":"2M","out":"video.mp4"}
		]
	}`)

	next, err := rewriteAria2ProxyRequest(body, "http://127.0.0.1:22334", "", 1)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	params := decoded["params"].([]any)
	options := params[1].(map[string]any)
	require.Equal(t, "1", options["split"])
	require.Equal(t, "1", options["max-connection-per-server"])
	require.Equal(t, "1024K", options["min-split-size"])
	require.Equal(t, "1024K", options["piece-length"])
	require.Equal(t, "600", options["timeout"])
	require.Equal(t, "video.mp4", options["out"])
}

func TestRewriteAria2ProxyRequestNormalizesTDLAddURIClientRange(t *testing.T) {
	body := []byte(`{
		"jsonrpc":"2.0",
		"id":"retry",
		"method":"aria2.addUri",
		"params":[
			["http://127.0.0.1:22334/download/document_1"],
			{"split":"1","max-connection-per-server":"1","out":"video.mp4"}
		]
	}`)

	next, err := rewriteAria2ProxyRequest(body, "http://127.0.0.1:22334", "", 4)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	params := decoded["params"].([]any)
	options := params[1].(map[string]any)
	require.Equal(t, "4", options["split"])
	require.Equal(t, "4", options["max-connection-per-server"])
	require.Equal(t, "1024K", options["min-split-size"])
	require.Equal(t, "1024K", options["piece-length"])
	require.Equal(t, "600", options["timeout"])
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

	next, err := rewriteAria2ProxyRequest(body, "http://127.0.0.1:22334", "", 4)
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
			{"methodName":"aria2.tellStatus","params":["` + testGID + `"]},
			{"methodName":"aria2.getOption","params":["` + testGID + `"]},
			{"methodName":"system.listMethods","params":[]}
		]]
	}`)

	next, err := rewriteAria2ProxyRequest(body, "", "secret", 1)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	params := decoded["params"].([]any)
	require.Len(t, params, 1)

	calls := params[0].([]any)
	require.Len(t, calls, 3)

	tellStatus := calls[0].(map[string]any)
	require.Equal(t, "aria2.tellStatus", tellStatus["methodName"])
	require.Equal(t, []any{testTokenSecret, testGID}, tellStatus["params"])

	getOption := calls[1].(map[string]any)
	require.Equal(t, "aria2.getOption", getOption["methodName"])
	require.Equal(t, []any{testTokenSecret, testGID}, getOption["params"])

	systemCall := calls[2].(map[string]any)
	require.Equal(t, "system.listMethods", systemCall["methodName"])
	require.Empty(t, systemCall["params"])
}

func TestInjectAria2SecretDoesNotAddTokenToSystemMethod(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"methods","method":"system.listMethods"}`)

	next, err := rewriteAria2ProxyRequest(body, "", "secret", 1)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	_, ok := decoded["params"]
	require.False(t, ok)
}

func TestInjectAria2SecretDoesNotDuplicateExistingToken(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"status","method":"aria2.tellStatus","params":["token:secret","` + testGID + `"]}`)

	next, err := rewriteAria2ProxyRequest(body, "", "secret", 1)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(next, &decoded))
	require.Equal(t, []any{"token:secret", testGID}, decoded["params"])
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
