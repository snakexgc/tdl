package aria2

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

const (
	testDownloadDir = "downloads"
	testSecret      = "secret"
	testTokenSecret = "token:secret"
	testGID1        = "gid-1"
)

func TestAria2AddURIWithoutSecret(t *testing.T) {
	t.Parallel()

	var reqBody aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"` + testGID1 + `"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	})

	gid, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{
		Dir: testDownloadDir,
		Out: "file.bin",
	})
	require.NoError(t, err)
	require.Equal(t, testGID1, gid)
	require.Equal(t, "aria2.addUri", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, []any{"http://example.com/file"}, reqBody.Params[0])
	require.Equal(t, map[string]any{
		aria2KeyDir:                 testDownloadDir,
		"out":                       "file.bin",
		"split":                     "1",
		"max-connection-per-server": "1",
		"min-split-size":            tdlAria2PieceSize,
		"piece-length":              tdlAria2PieceSize,
		"timeout":                   "600",
		"continue":                  aria2BoolTrue,
		"allow-piece-length-change": aria2BoolTrue,
		"allow-overwrite":           aria2BoolTrue,
		"auto-file-renaming":        aria2BoolFalse,
		"user-agent":                tdlAria2UserAgent,
	}, reqBody.Params[1])
}

func TestAria2AddURIWithClientRangeConnections(t *testing.T) {
	t.Parallel()

	var reqBody aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"` + testGID1 + `"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		TimeoutSeconds: 5,
	})

	_, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{
		Connections: 4,
	})
	require.NoError(t, err)
	options := reqBody.Params[1].(map[string]any)
	require.Equal(t, "4", options["split"])
	require.Equal(t, "4", options["max-connection-per-server"])
	require.Equal(t, tdlAria2PieceSize, options["min-split-size"])
	require.Equal(t, tdlAria2PieceSize, options["piece-length"])
	require.Equal(t, "600", options["timeout"])
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
		Secret:         testSecret,
		TimeoutSeconds: 5,
	})

	gid, err := client.AddURI(context.Background(), "http://example.com/file", aria2AddURIOptions{})
	require.NoError(t, err)
	require.Equal(t, "gid-2", gid)
	require.Len(t, reqBody.Params, 3)
	require.Equal(t, testTokenSecret, reqBody.Params[0])
	require.Equal(t, []any{"http://example.com/file"}, reqBody.Params[1])
	require.Equal(t, map[string]any{
		"split":                     "1",
		"max-connection-per-server": "1",
		"min-split-size":            tdlAria2PieceSize,
		"piece-length":              tdlAria2PieceSize,
		"timeout":                   "600",
		"continue":                  aria2BoolTrue,
		"allow-piece-length-change": aria2BoolTrue,
		"allow-overwrite":           aria2BoolTrue,
		"auto-file-renaming":        aria2BoolFalse,
		"user-agent":                tdlAria2UserAgent,
	}, reqBody.Params[2])
}

func TestAria2AddTorrentWithOptions(t *testing.T) {
	t.Parallel()

	var reqBody aria2RPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"torrent-gid"}`))
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		Secret:         testSecret,
		TimeoutSeconds: 5,
	})

	gid, err := client.AddTorrent(context.Background(), []byte("torrent"), aria2AddURIOptions{
		Dir: testDownloadDir,
		Out: "file.torrent",
	})
	require.NoError(t, err)
	require.Equal(t, "torrent-gid", gid)
	require.Equal(t, "aria2.addTorrent", reqBody.Method)
	require.Equal(t, testTokenSecret, reqBody.Params[0])
	require.Equal(t, "dG9ycmVudA==", reqBody.Params[1])
	require.Equal(t, []any{}, reqBody.Params[2])
	require.Equal(t, map[string]any{
		aria2KeyDir: testDownloadDir,
		"out":       "file.torrent",
	}, reqBody.Params[3])
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
		Secret:         testSecret,
		TimeoutSeconds: 5,
	})

	err := client.SetMaxConcurrentDownloads(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "aria2.changeGlobalOption", reqBody.Method)
	require.Len(t, reqBody.Params, 2)
	require.Equal(t, testTokenSecret, reqBody.Params[0])
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
		case "aria2.getGlobalOption":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":{"dir":"/root/download"}}`))
		case "aria2.tellActive":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":[{"gid":"active-1","status":"active","totalLength":"100","completedLength":"40","files":[{"uris":[{"uri":"http://example.com/a"}]}]}]}`))
		case "aria2.tellWaiting":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":[{"gid":"waiting-1","status":"waiting","files":[{"uris":[{"uri":"http://example.com/b"}]}]}]}`))
		case "aria2.tellStopped":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":[{"gid":"stopped-1","status":"error","errorMessage":"gone","files":[{"uris":[{"uri":"http://example.com/c"}]}]}]}`))
		case "aria2.forcePause":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"` + testGID1 + `"}`))
		case "aria2.unpause":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"` + testGID1 + `"}`))
		case "aria2.removeDownloadResult":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"tdl-watch","result":"OK"}`))
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer srv.Close()

	client := newAria2Client(config.Aria2Config{
		RPCURL:         srv.URL,
		Secret:         testSecret,
		TimeoutSeconds: 5,
	})

	dir, err := client.GetGlobalDir(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/root/download", dir)

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

	require.NoError(t, client.ForcePause(context.Background(), testGID1))
	require.NoError(t, client.Unpause(context.Background(), testGID1))
	require.NoError(t, client.RemoveDownloadResult(context.Background(), testGID1))

	require.Len(t, requests, 7)
	require.Equal(t, "aria2.getGlobalOption", requests[0].Method)
	require.Equal(t, []any{testTokenSecret}, requests[0].Params)
	require.Equal(t, "aria2.tellActive", requests[1].Method)
	require.Equal(t, testTokenSecret, requests[1].Params[0])
	require.Equal(t, stringSliceToAny(aria2StatusKeys), requests[1].Params[1])
	require.Equal(t, "aria2.tellWaiting", requests[2].Method)
	require.Equal(t, testTokenSecret, requests[2].Params[0])
	require.Equal(t, float64(5), requests[2].Params[1])
	require.Equal(t, float64(2), requests[2].Params[2])
	require.Equal(t, stringSliceToAny(aria2StatusKeys), requests[2].Params[3])
	require.Equal(t, "aria2.tellStopped", requests[3].Method)
	require.Equal(t, testTokenSecret, requests[3].Params[0])
	require.Equal(t, float64(7), requests[3].Params[1])
	require.Equal(t, float64(3), requests[3].Params[2])
	require.Equal(t, stringSliceToAny(aria2StatusKeys), requests[3].Params[3])
	require.Equal(t, "aria2.forcePause", requests[4].Method)
	require.Equal(t, []any{testTokenSecret, testGID1}, requests[4].Params)
	require.Equal(t, "aria2.unpause", requests[5].Method)
	require.Equal(t, []any{testTokenSecret, testGID1}, requests[5].Params)
	require.Equal(t, "aria2.removeDownloadResult", requests[6].Method)
	require.Equal(t, []any{testTokenSecret, testGID1}, requests[6].Params)
}

func TestWaitForAria2RetriesConnectionErrors(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ConcurrentDownloadSetter{
		errs: []error{
			fakeAria2ConnectionError(),
			fakeAria2ConnectionError(),
		},
	}

	err := retryAria2ConnectionWithInterval(context.Background(), zap.NewNop(), "set aria2 max concurrent downloads", time.Millisecond, func() error {
		return client.SetMaxConcurrentDownloads(context.Background(), 3)
	})
	require.NoError(t, err)
	require.Equal(t, 3, client.calls)
	require.Equal(t, []int{3, 3, 3}, client.limits)
}

func TestWaitForAria2DoesNotRetryRPCError(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ConcurrentDownloadSetter{
		errs: []error{gferrors.New("aria2 rpc error 1: unauthorized")},
	}

	err := retryAria2ConnectionWithInterval(context.Background(), zap.NewNop(), "set aria2 max concurrent downloads", time.Millisecond, func() error {
		return client.SetMaxConcurrentDownloads(context.Background(), 3)
	})
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
