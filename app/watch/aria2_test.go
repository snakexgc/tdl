package watch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/config"
)

func TestAria2AddURIWithoutSecret(t *testing.T) {
	t.Parallel()

	var reqBody aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"gid-1"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	})

	gid, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{
		Dir: "downloads",
		Out: "file.bin",
	})
	require.NoError(t, err)
	require.Equal(t, "gid-1", gid)
	require.Equal(t, "aria2.addUri", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, []any{"http://example.com/file"}, reqBody.Params[0])
	require.Equal(t, map[string]any{
		"dir":                       "downloads",
		"out":                       "file.bin",
		"split":                     "1",
		"max-connection-per-server": "1",
		"allow-piece-length-change": "true",
		"allow-overwrite":           "true",
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-watch-aria2",
	}, reqBody.Params[1])
}

func TestAria2AddURIWithSecret(t *testing.T) {
	t.Parallel()

	var reqBody aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"gid-2"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		Secret:         "secret",
		TimeoutSeconds: 5,
	})

	gid, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{})
	require.NoError(t, err)
	require.Equal(t, "gid-2", gid)
	require.Len(t, reqBody.Params, 3)
	require.Equal(t, "token:secret", reqBody.Params[0])
	require.Equal(t, []any{"http://example.com/file"}, reqBody.Params[1])
	require.Equal(t, map[string]any{
		"split":                     "1",
		"max-connection-per-server": "1",
		"allow-piece-length-change": "true",
		"allow-overwrite":           "true",
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-watch-aria2",
	}, reqBody.Params[2])
}

func TestAria2AddURIErrorResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","error":{"code":1,"message":"boom"}}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	})

	_, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{})
	require.Error(t, err)
	require.ErrorContains(t, err, "boom")
}

func TestAria2AddURITimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"gid"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 0,
	})
	client.httpClient.Timeout = 50 * time.Millisecond

	_, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{})
	require.Error(t, err)
	require.ErrorContains(t, err, "Client.Timeout")
}
