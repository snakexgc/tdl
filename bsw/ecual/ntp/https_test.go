package ntp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

func TestHTTPSTimeChecksFreshHeaders(t *testing.T) {
	for _, mode := range []string{validResponse, "missing_date", "invalid_date", "cached", "bad_status"} {
		t.Run(mode, func(t *testing.T) {
			var validRequest atomic.Bool
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				validRequest.Store(r.Method == http.MethodHead && r.URL.Query().Get("tdl_time") != "" && r.Header.Get("Cache-Control") == "no-cache, no-store, max-age=0")
				w.Header().Set("Date", time.Now().UTC().Add(time.Hour).Format(http.TimeFormat))
				switch mode {
				case "missing_date":
					w.Header()["Date"] = nil
				case "invalid_date":
					w.Header().Set("Date", "invalid")
				case "cached":
					w.Header().Set("Age", "60")
				case "bad_status":
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer origin.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			sample, err := queryHTTPSWithClient(ctx, origin.URL, origin.Client())
			require.True(t, validRequest.Load())
			if mode == validResponse {
				require.NoError(t, err)
				require.Equal(t, types.ClockSourceHTTPS, sample.Source)
				require.InDelta(t, float64(time.Hour), float64(sample.Offset), float64(time.Second))
				require.Equal(t, origin.URL, sample.Server)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestHTTPSTimeUsesAuthenticatedCONNECTProxy(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", time.Now().UTC().Add(time.Hour).Format(http.TimeFormat))
	}))
	defer origin.Close()
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			http.Error(w, "unreachable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		calls.Add(1)
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffered); close(done) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	}))
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, transport, err := newHTTPSClient(ctx, "http://user:secret@"+proxy.Listener.Addr().String())
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	// Trust only the test origin's certificate; production uses the system roots.
	transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	sample, err := queryHTTPSWithClient(ctx, origin.URL, client)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, types.ClockSourceHTTPS, sample.Source)
}

func TestHTTPSTimeDoesNotBypassFailedProxyOrTLS(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, transport, err := newHTTPSClient(ctx, "http://user:secret@"+address)
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	_, err = queryHTTPSWithClient(ctx, origin.URL, client)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
	require.Zero(t, calls.Load())
	_, err = queryHTTPSWithClient(ctx, origin.URL, &http.Client{})
	require.Error(t, err, "untrusted origin certificate must fail")
	require.Zero(t, calls.Load())
}

func TestHTTPSCancellationAndInvalidProxy(t *testing.T) {
	entered := make(chan struct{})
	origin := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer origin.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := queryHTTPSWithClient(ctx, origin.URL, origin.Client()); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTPS query did not reach origin")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("HTTPS query was not cancelled")
	}
	for _, url := range []string{"invalid", "http://user:secret@", "ftp://user:secret@proxy.test:21"} {
		_, err := (Client{}).QueryViaProxy(context.Background(), "time.google.com", url, time.Second)
		require.Error(t, err, fmt.Sprint(url))
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestHTTPProxyHandshakeTimeoutClosesConnection(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan error, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			closed <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		close(entered)
		// Stall CONNECT before any response; cancellation must close the socket.
		_, err = conn.Read(make([]byte, 1))
		if !errors.Is(err, io.EOF) {
			closed <- fmt.Errorf("stalled proxy connection was not closed: %w", err)
			return
		}
		closed <- nil
	}))
	defer proxy.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, transport, err := newHTTPSClient(ctx, proxy.URL)
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	result := make(chan error, 1)
	go func() { _, err := queryHTTPSWithClient(ctx, "https://time.test.invalid/", client); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("proxy CONNECT was not attempted")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("proxy CONNECT did not stop")
	}
	require.NoError(t, <-closed)
}
