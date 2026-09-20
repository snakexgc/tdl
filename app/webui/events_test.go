package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
)

func TestEventsAuthenticationOriginSubscriptionAndModeSwitch(t *testing.T) {
	cfg := config.DefaultConfig()
	source := config.NewSource(cfg)
	s := NewServer(Options{Context: config.WithSource(context.Background(), source)})
	s.sessions["test-session"] = time.Now().Add(time.Hour)
	server := httptest.NewServer(s.routes())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/events"
	_, response, err := websocket.Dial(ctx, url, nil)
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	headers := http.Header{"Cookie": {webUICookieName + "=test-session"}, "Origin": {"https://foreign.example"}}
	_, response, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	headers.Set("Origin", server.URL)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	require.NoError(t, err)
	defer conn.CloseNow()
	var packet struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	require.NoError(t, wsjson.Read(ctx, conn, &packet))
	require.Equal(t, "status", packet.Topic)
	require.Contains(t, string(packet.Data), `"mode":"aria2"`)
	next, err := config.Clone(cfg)
	require.NoError(t, err)
	next.Downloader.Executors = []string{localDownloadExecutor}
	source.Replace(next)
	for {
		require.NoError(t, wsjson.Read(ctx, conn, &packet))
		if strings.Contains(string(packet.Data), `"mode":"local"`) {
			break
		}
	}
	// Revoking the session also closes an already upgraded connection.
	s.sessionMu.Lock()
	delete(s.sessions, "test-session")
	s.sessionMu.Unlock()
	err = wsjson.Read(ctx, conn, &packet)
	require.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(err))
}
