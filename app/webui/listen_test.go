package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	downloadlisten "github.com/snakexgc/tdl/application/download.listen"
	listenstore "github.com/snakexgc/tdl/bsw/cdd/listen"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

type listenDirectory struct{ *rte.Directory }

const (
	listenKindGroup   = "group"
	listenKindChannel = "channel"
	fieldUser         = "user"
)

func (d listenDirectory) ComponentConfigurations() ([]rte.Configuration, bool) {
	return d.Configurations(context.Background()), true
}

func (d listenDirectory) SaveComponentConfiguration(ctx context.Context, id string, values map[string]any) error {
	return d.Patch(ctx, id, values)
}

func (d listenDirectory) SaveComponentConfigurationVersion(ctx context.Context, id string, values map[string]any, revision string) error {
	return d.PatchWithRevision(ctx, id, values, revision)
}

type listenDialogs struct{}

func (listenDialogs) Dialogs(context.Context) ([]types.Dialog, error) {
	return []types.Dialog{
		{Ref: "chat:101", Title: "文件分享群", Kind: listenKindGroup},
		{Ref: "channel:102", Title: "讨论超级群", Username: "discussion", Kind: listenKindGroup},
		{Ref: "channel:103", Title: "视频频道", Username: "videochannel", Kind: listenKindChannel},
		{Ref: "user:105", Title: "文件机器人", Username: "filesbot", Kind: "bot"},
		{Ref: "user:106", Title: testUserAlice, Username: testLogAccount, Kind: fieldUser},
	}, nil
}

func listenWebServer(t *testing.T) (*Server, ports.DownloadListener, listenDirectory) {
	t.Helper()
	initWebUITestConfig(t)
	ctx := config.WithSource(context.Background(), config.NewSource(config.DefaultConfig()))
	configuration, err := configuration.Open(ctx, t.TempDir())
	require.NoError(t, err)
	store := configuration.Store()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	ids := []string{}
	for _, definition := range catalog.Definitions() {
		ids = append(ids, definition.Manifest.ID)
	}
	snapshot, err := store.Snapshot(ctx, ids)
	require.NoError(t, err)
	host, listener, err := application.DownloadListenHost(ctx, types.DefaultAccount, listenstore.New(&storage.Memory{}), types.ListenFilters{DownloadEnabled: true, Include: []string{"mp4"}, MinMB: 1, Limit: 2, DCConnections: 3}, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	directory := listenDirectory{rte.NewDirectory(catalog, store).WithActiveStore(snapshot)}
	require.NoError(t, directory.Bind("listen", func() *rte.Runtime { return host }))
	server := NewServer(Options{Context: ctx, Catalog: catalog, ComponentManager: directory, ComponentStore: store, Namespace: "default", ConfigurationManager: configuration, Dialogs: listenDialogs{}})
	server.sessions["listen-session"] = time.Now().Add(time.Hour)
	return server, listener, directory
}

func TestListenRoutesAuthenticatePersistApplyAndRejectStaleRevision(t *testing.T) {
	server, listener, directory := listenWebServer(t)
	handler := server.routes()
	request := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			req.AddCookie(&http.Cookie{Name: webUICookieName, Value: "listen-session"})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	require.Equal(t, http.StatusUnauthorized, request(http.MethodGet, "/api/listen-downloads/status", "", false).Code)
	for _, path := range []string{"/listen-downloads", "/views/listen-downloads.html", "/static/js/listen-downloads.js", "/static/css/listen-downloads.css"} {
		require.Equal(t, http.StatusOK, request(http.MethodGet, path, "", true).Code)
	}
	var component rte.Configuration
	for _, entry := range directory.Configurations(context.Background()) {
		if entry.ID == downloadlisten.ID {
			component = entry
		}
	}
	body, err := json.Marshal(map[string]any{"id": downloadlisten.ID, "revision": component.Revision, "values": map[string]any{"rules": []types.DownloadListenRule{{ID: "web-rule", Enabled: true, Sources: []types.ChatRef{"channel:103"}, ListenComments: true}}}})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, request(http.MethodPatch, "/api/components", string(body), true).Code)
	require.Contains(t, request(http.MethodGet, "/api/listen-downloads/status", "", true).Body.String(), "channel:103")
	require.Equal(t, http.StatusConflict, request(http.MethodPatch, "/api/components", string(body), true).Code)
	for _, entry := range directory.Configurations(context.Background()) {
		if entry.ID == downloadlisten.ID {
			require.False(t, entry.PendingRestart)
		}
	}
	require.NotNil(t, listener)
}

// This explicit fixture serves the actual embedded WebUI and persistent config,
// while every Telegram event/download remains synthetic and account-free.
func TestListenBrowserFixture(t *testing.T) {
	if os.Getenv("TDL_LISTEN_BROWSER_FIXTURE") != "1" {
		t.Skip("explicit local browser fixture")
	}
	server, listener, _ := listenWebServer(t)
	transport := &listenBrowserTransport{messages: map[string]types.ListenMessage{}, tasks: map[string]types.DownloadTask{}}
	server.opts.DownloadControl = transport
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Serve(ctx, ports.ListenResources{Transport: transport, Downloads: transport, Enabled: func() bool { return true }})
	}()
	t.Cleanup(func() { cancel(); <-done })
	handler := server.routes()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/login" {
			http.SetCookie(w, &http.Cookie{Name: webUICookieName, Value: "listen-session", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/listen-downloads", http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/__fixture/event" && r.Method == http.MethodPost {
			if !server.sessionOK(r) {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			var message types.ListenMessage
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			message.Date = time.Now().Unix()
			transport.mu.Lock()
			transport.messages[fmt.Sprintf("%s:%d/%d", message.Peer.Kind, message.Peer.ID, message.ID)] = message
			transport.mu.Unlock()
			if err := listener.Observe(r.Context(), message); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if r.URL.Path == "/__fixture/task" && r.Method == http.MethodPost {
			if !server.sessionOK(r) {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			var task types.DownloadTask
			if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			transport.mu.Lock()
			transport.tasks[task.TaskID] = task
			transport.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer fixture.Close()
	fmt.Printf("LISTEN_BROWSER_URL=%s/__fixture/login\n", fixture.URL)
	if stopPath := os.Getenv("TDL_LISTEN_BROWSER_STOP"); stopPath != "" {
		deadline := time.NewTimer(15 * time.Minute)
		defer deadline.Stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-deadline.C:
				return
			case <-ticker.C:
				if _, err := os.Stat(stopPath); err == nil {
					return
				}
			}
		}
	}
	<-time.After(10 * time.Minute)
}

type listenBrowserTransport struct {
	mu       sync.Mutex
	messages map[string]types.ListenMessage
	tasks    map[string]types.DownloadTask
}

func (*listenBrowserTransport) ResolveSource(_ context.Context, ref types.ChatRef, _ bool) (types.ListenPeer, error) {
	var kind string
	var id int64
	parts := strings.Split(string(ref), ":")
	if len(parts) != 2 {
		return types.ListenPeer{}, ports.ErrListenInaccessible
	}
	kind = parts[0]
	if _, err := fmt.Sscan(parts[1], &id); err != nil {
		return types.ListenPeer{}, err
	}
	result := types.ListenPeer{Peer: types.MessagePeer{Kind: kind, ID: id}, Title: string(ref), Broadcast: id == 103}
	if id == 103 {
		result.Discussion = types.MessagePeer{Kind: listenKindChannel, ID: 104}
	}
	return result, nil
}

func (t *listenBrowserTransport) History(_ context.Context, peer types.MessagePeer, offset, minID int) (types.ListenPage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	page := types.ListenPage{Done: true}
	for _, message := range t.messages {
		if message.Peer.Kind == peer.Kind && message.Peer.ID == peer.ID && message.ID > minID && (offset == 0 || message.ID < offset) {
			page.Messages = append(page.Messages, message)
		}
	}
	sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].ID > page.Messages[j].ID })
	return page, nil
}

func (*listenBrowserTransport) Discussion(_ context.Context, _ types.MessagePeer, id int) (types.ListenThreadRecord, error) {
	return types.ListenThreadRecord{Peer: types.MessagePeer{Kind: listenKindChannel, ID: 104}, RootID: id + 1000}, nil
}

func (*listenBrowserTransport) Replies(context.Context, types.MessagePeer, int, int, int) (types.ListenPage, error) {
	return types.ListenPage{Done: true}, nil
}

func (t *listenBrowserTransport) Message(_ context.Context, peer types.MessagePeer, id int) (types.ListenMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	message, exists := t.messages[fmt.Sprintf("%s:%d/%d", peer.Kind, peer.ID, id)]
	if !exists {
		return types.ListenMessage{}, ports.ErrListenDeleted
	}
	return message, nil
}

func (t *listenBrowserTransport) Submit(_ context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	message := t.messages[fmt.Sprintf("%s:%d/%d", request.Peer.Kind, request.Peer.ID, request.MessageID)]
	if t.tasks == nil {
		t.tasks = map[string]types.DownloadTask{}
	}
	t.tasks[request.ExpectedMediaID] = types.DownloadTask{Account: types.DefaultAccount, Executor: "local", ID: request.ExpectedMediaID, TaskID: request.ExpectedMediaID, FileName: message.FileName, Total: message.FileSize, State: types.DownloadQueued, Status: string(types.DownloadQueued), CreatedAt: time.Now()}
	return types.DownloadSubmissionSummary{Total: 1, Queued: 1, MessageID: request.MessageID}, nil
}

func (t *listenBrowserTransport) Tasks(_ context.Context, executor string) ([]types.DownloadTask, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := []types.DownloadTask{}
	for _, task := range t.tasks {
		if executor == "" || executor == task.Executor {
			items = append(items, task)
		}
	}
	return items, nil
}

func (*listenBrowserTransport) Control(context.Context, types.DownloadAction) (types.DownloadActionResult, error) {
	return types.DownloadActionResult{}, nil
}
