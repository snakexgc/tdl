package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type testSource struct{ transfer *testTransfer }

func (s testSource) Open(context.Context, string) (types.RangeResource, ports.RangeTransfer, bool, error) {
	return types.RangeResource{ID: "file", FileName: "file.bin", FileSize: 4, Available: true}, s.transfer, true, nil
}

type maintainedSource struct {
	testSource
	entered, release chan struct{}
	cleaned          atomic.Bool
}

func (s *maintainedSource) CleanupExpired(ctx context.Context) error {
	close(s.entered)
	<-ctx.Done()
	<-s.release
	return nil
}
func (s *maintainedSource) CleanupSources(context.Context) error { s.cleaned.Store(true); return nil }

func TestRangeMaintenanceDrainsBeforeSourceCleanup(t *testing.T) {
	source := &maintainedSource{entered: make(chan struct{}), release: make(chan struct{})}
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, New(source, nil, time.Second)))
	host, err := registry.Build("account", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	require.False(t, source.cleaned.Load())
	close(source.release)
	require.NoError(t, host.Stop(context.Background()))
	require.True(t, source.cleaned.Load())
}

type testTransfer struct {
	stream  func(context.Context, io.Writer) error
	reports int
	closed  int
}

func (s *testTransfer) Close() error { s.closed++; return nil }

func (*testTransfer) Ready(context.Context) error { return nil }

type testLease struct{}

func (testLease) Release()                                                 {}
func (*testTransfer) Acquire(context.Context) (ports.DownloadLease, error) { return testLease{}, nil }
func (s *testTransfer) Stream(ctx context.Context, _ ports.DownloadLease, _, _ int64, w io.Writer) error {
	return s.stream(ctx, w)
}
func (s *testTransfer) Report(context.Context, []types.ByteRange) error { s.reports++; return nil }

func TestRangeComponentCancelsAndDrainsRequests(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	transport := &testTransfer{stream: func(ctx context.Context, _ io.Writer) error {
		close(entered)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}}
	handler := New(testSource{transport}, nil, time.Second)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, handler))
	host, err := registry.Build("alice", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/download/file", nil))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	close(release)
	<-done
	require.NoError(t, host.Stop(context.Background()))
	require.Zero(t, transport.reports)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/download/file", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestHeadDoesNotAcquireOrStream(t *testing.T) {
	transport := &testTransfer{}
	handler := New(testSource{transport}, nil, time.Second)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/download/file", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "4", response.Header().Get("Content-Length"))
	require.Empty(t, response.Body.String())
	require.Equal(t, 1, transport.closed)
}

func TestRangeClosesSourceOnInvalidRange(t *testing.T) {
	transport := &testTransfer{}
	handler := New(testSource{transport}, nil, time.Second)
	request := httptest.NewRequest(http.MethodGet, "/download/file", nil)
	request.Header.Set("Range", "bytes=10-20")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, response.Code)
	require.Equal(t, 1, transport.closed)
}

func TestHeadIgnoresRangeAndReturnsFullLength(t *testing.T) {
	for _, header := range []string{"bytes=1-2", "bytes=0-0,3-3", "bytes=10-20", "invalid"} {
		t.Run(header, func(t *testing.T) {
			transport := &testTransfer{}
			handler := New(testSource{transport}, nil, time.Second)
			request := httptest.NewRequest(http.MethodHead, "/download/file", nil)
			request.Header.Set("Range", header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, "4", response.Header().Get("Content-Length"))
			require.Empty(t, response.Header().Get("Content-Range"))
			require.Empty(t, response.Body.String())
			require.Equal(t, 1, transport.closed)
			require.Zero(t, transport.reports)
		})
	}
}
