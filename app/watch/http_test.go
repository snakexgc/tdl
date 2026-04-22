package watch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/pkg/config"
)

func TestBuildDownloadURL(t *testing.T) {
	t.Parallel()

	got, err := buildDownloadURL("http://127.0.0.1:8080/base", "task-1")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080/base/download/task-1", got)
}

func TestTaskStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTaskStore()
	task := &downloadTask{ID: "task-1", FileName: "a.bin"}
	store.Add(task)

	got, ok := store.Get("task-1")
	require.True(t, ok)
	require.Equal(t, task, got)
}

func TestTaskIDUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]struct{}{}
	for i := 0; i < 10; i++ {
		id, err := newTaskID()
		require.NoError(t, err)
		_, exists := seen[id]
		require.False(t, exists)
		seen[id] = struct{}{}
	}
}

func TestDownloadHandlerSuccessAndRange(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, &poolHolder{}, nil)

	payload := []byte("0123456789")
	proxy.stream = func(ctx context.Context, task *downloadTask, start, end int64, w io.Writer) error {
		_, err := w.Write(payload[start : end+1])
		return err
	}

	task := &downloadTask{
		ID:       "task-1",
		FileName: "file.bin",
		FileSize: int64(len(payload)),
		Media:    &tmedia.Media{Name: "file.bin", Size: int64(len(payload))},
	}
	proxy.tasks.Add(task)

	req := httptest.NewRequest(http.MethodGet, "/download/task-1", nil)
	rec := httptest.NewRecorder()
	proxy.handleDownload(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, payload, body)
	require.Equal(t, "bytes", res.Header.Get("Accept-Ranges"))

	rangeReq := httptest.NewRequest(http.MethodGet, "/download/task-1", nil)
	rangeReq.Header.Set("Range", "bytes=2-5")
	rangeRec := httptest.NewRecorder()
	proxy.handleDownload(rangeRec, rangeReq)
	rangeRes := rangeRec.Result()
	rangeBody, err := io.ReadAll(rangeRes.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusPartialContent, rangeRes.StatusCode)
	require.Equal(t, []byte("2345"), rangeBody)
	require.Equal(t, "bytes 2-5/10", rangeRes.Header.Get("Content-Range"))
}

func TestDownloadHandlerMissingTask(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, &poolHolder{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/download/task-1", nil)
	rec := httptest.NewRecorder()
	proxy.handleDownload(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Result().StatusCode)
}

func TestDownloadHandlerInvalidRange(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, &poolHolder{}, nil)

	task := &downloadTask{
		ID:       "task-1",
		FileName: "file.bin",
		FileSize: 10,
		Media:    &tmedia.Media{Name: "file.bin", Size: 10},
	}
	proxy.tasks.Add(task)

	req := httptest.NewRequest(http.MethodGet, "/download/task-1", nil)
	req.Header.Set("Range", "bytes=20-30")
	rec := httptest.NewRecorder()
	proxy.handleDownload(rec, req)

	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Result().StatusCode)
}

func TestDownloadHandlerHead(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, &poolHolder{}, nil)

	called := false
	proxy.stream = func(ctx context.Context, task *downloadTask, start, end int64, w io.Writer) error {
		called = true
		return nil
	}

	task := &downloadTask{
		ID:       "task-1",
		FileName: "file.bin",
		FileSize: 10,
		Media:    &tmedia.Media{Name: "file.bin", Size: 10},
	}
	proxy.tasks.Add(task)

	req := httptest.NewRequest(http.MethodHead, "/download/task-1", nil)
	rec := httptest.NewRecorder()
	proxy.handleDownload(rec, req)

	require.Equal(t, http.StatusOK, rec.Result().StatusCode)
	require.False(t, called)
	require.Equal(t, "10", rec.Result().Header.Get("Content-Length"))
}
