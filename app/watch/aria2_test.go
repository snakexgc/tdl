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

func TestAria2TellAndPauseMethods(t *testing.T) {
	t.Parallel()

	var requests []aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req aria2RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "aria2.tellActive":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":[{"gid":"active-1","status":"active","totalLength":"100","completedLength":"40","files":[{"uris":[{"uri":"http://example.com/a"}]}]}]}`))
		case "aria2.tellWaiting":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":[{"gid":"waiting-1","status":"waiting","files":[{"uris":[{"uri":"http://example.com/b"}]}]}]}`))
		case "aria2.tellStopped":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":[{"gid":"stopped-1","status":"error","errorMessage":"gone","files":[{"uris":[{"uri":"http://example.com/c"}]}]}]}`))
		case "aria2.forcePause":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"gid-1"}`))
		case "aria2.unpause":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"gid-1"}`))
		case "aria2.removeDownloadResult":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"OK"}`))
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		Secret:         "secret",
		TimeoutSeconds: 5,
	})

	active, err := client.TellActive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "active-1", active[0].GID)
	require.Equal(t, "100", active[0].TotalLength)
	require.Equal(t, "40", active[0].CompletedLength)
	require.Equal(t, "http://example.com/a", active[0].Files[0].URIs[0].URI)

	waiting, err := client.TellWaiting(context.Background(), 5, 2)
	require.NoError(t, err)
	require.Equal(t, "waiting-1", waiting[0].GID)
	require.Equal(t, "http://example.com/b", waiting[0].Files[0].URIs[0].URI)

	stopped, err := client.TellStopped(context.Background(), 7, 3)
	require.NoError(t, err)
	require.Equal(t, "stopped-1", stopped[0].GID)
	require.Equal(t, "gone", stopped[0].ErrorMessage)

	require.NoError(t, client.ForcePause(context.Background(), "gid-1"))
	require.NoError(t, client.Unpause(context.Background(), "gid-1"))
	require.NoError(t, client.RemoveDownloadResult(context.Background(), "gid-1"))

	require.Len(t, requests, 6)
	require.Equal(t, "aria2.tellActive", requests[0].Method)
	require.Equal(t, "token:secret", requests[0].Params[0])
	require.Equal(t, stringSliceToAny(aria2StatusKeys), requests[0].Params[1])
	require.Equal(t, "aria2.tellWaiting", requests[1].Method)
	require.Equal(t, "token:secret", requests[1].Params[0])
	require.Equal(t, float64(5), requests[1].Params[1])
	require.Equal(t, float64(2), requests[1].Params[2])
	require.Equal(t, stringSliceToAny(aria2StatusKeys), requests[1].Params[3])
	require.Equal(t, "aria2.tellStopped", requests[2].Method)
	require.Equal(t, "token:secret", requests[2].Params[0])
	require.Equal(t, float64(7), requests[2].Params[1])
	require.Equal(t, float64(3), requests[2].Params[2])
	require.Equal(t, stringSliceToAny(aria2StatusKeys), requests[2].Params[3])
	require.Equal(t, "aria2.forcePause", requests[3].Method)
	require.Equal(t, []any{"token:secret", "gid-1"}, requests[3].Params)
	require.Equal(t, "aria2.unpause", requests[4].Method)
	require.Equal(t, []any{"token:secret", "gid-1"}, requests[4].Params)
	require.Equal(t, "aria2.removeDownloadResult", requests[5].Method)
	require.Equal(t, []any{"token:secret", "gid-1"}, requests[5].Params)
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

func stringSliceToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
