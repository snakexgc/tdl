package watch

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	gferrors "github.com/go-faster/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

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
		Dir:         "downloads",
		Out:         "file.bin",
		Connections: 4,
	})
	require.NoError(t, err)
	require.Equal(t, "gid-1", gid)
	require.Equal(t, "aria2.addUri", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, []any{"http://example.com/file"}, reqBody.Params[0])
	require.Equal(t, map[string]any{
		"dir":                       "downloads",
		"out":                       "file.bin",
		"split":                     "4",
		"max-connection-per-server": "4",
		"continue":                  "true",
		"min-split-size":            "1M",
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
		"continue":                  "true",
		"allow-piece-length-change": "true",
		"allow-overwrite":           "true",
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-watch-aria2",
	}, reqBody.Params[2])
}

func TestAria2SetMaxConcurrentDownloads(t *testing.T) {
	t.Parallel()

	var reqBody aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"OK"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		Secret:         "secret",
		TimeoutSeconds: 5,
	})

	err := client.SetMaxConcurrentDownloads(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "aria2.changeGlobalOption", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, "token:secret", reqBody.Params[0])
	require.Equal(t, map[string]any{
		"max-concurrent-downloads": "7",
	}, reqBody.Params[1])
}

func TestAria2SetMaxConcurrentDownloadsInvalidLimit(t *testing.T) {
	t.Parallel()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         "http://127.0.0.1:6800/jsonrpc",
		TimeoutSeconds: 5,
	})

	err := client.SetMaxConcurrentDownloads(context.Background(), 0)
	require.Error(t, err)
	require.ErrorContains(t, err, "greater than 0")
}

func TestWaitForAria2RetriesConnectionErrors(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ConcurrentDownloadSetter{
		errs: []error{
			fakeAria2ConnectionError(),
			fakeAria2ConnectionError(),
		},
	}

	err := waitForAria2(context.Background(), client, 3, time.Millisecond, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, 3, client.calls)
	require.Equal(t, []int{3, 3, 3}, client.limits)
}

func TestWaitForAria2DoesNotRetryRPCError(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ConcurrentDownloadSetter{
		errs: []error{gferrors.New("aria2 rpc error 1: unauthorized")},
	}

	err := waitForAria2(context.Background(), client, 3, time.Millisecond, zap.NewNop())
	require.Error(t, err)
	require.ErrorContains(t, err, "unauthorized")
	require.Equal(t, 1, client.calls)
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

type fakeAria2ConcurrentDownloadSetter struct {
	calls  int
	limits []int
	errs   []error
}

func (f *fakeAria2ConcurrentDownloadSetter) SetMaxConcurrentDownloads(ctx context.Context, limit int) error {
	f.calls++
	f.limits = append(f.limits, limit)
	if len(f.errs) == 0 {
		return nil
	}

	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func fakeAria2ConnectionError() error {
	return gferrors.Wrap(&url.Error{
		Op:  "Post",
		URL: "http://127.0.0.1:6800/jsonrpc",
		Err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: gferrors.New("connection refused"),
		},
	}, "do aria2 request")
}
