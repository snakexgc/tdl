package watch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/storage"
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

	store := newTaskStore(nil)
	task := &downloadTask{ID: "task-1", FileName: "a.bin"}
	require.NoError(t, store.Add(context.Background(), task))

	got, ok, err := store.Get(context.Background(), "task-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task, got)
}

func TestDownloadTaskIDStableForMedia(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, &poolHolder{}, nil, nil)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{
			ID:            12345,
			AccessHash:    67890,
			FileReference: []byte("ref"),
		},
		Name: "file.bin",
		Size: 10,
		DC:   2,
	}

	first, err := proxy.NewTask(context.Background(), 1, 2, "first.bin", 10, media)
	require.NoError(t, err)
	second, err := proxy.NewTask(context.Background(), 3, 4, "second.bin", 10, media)
	require.NoError(t, err)

	require.Equal(t, "document_12345", first.ID)
	require.Equal(t, first.ID, second.ID)

	u, err := proxy.BuildURL(first.ID)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080/download/document_12345", u)
}

func TestTaskStoreRestoresPersistentTask(t *testing.T) {
	t.Parallel()

	kvd := newMemoryTaskStorage()
	original := newTaskStore(kvd)
	task := &downloadTask{
		ID:        "photo_42_y",
		PeerID:    100,
		MessageID: 200,
		FileName:  "photo.jpg",
		FileSize:  10,
		Media: &tmedia.Media{
			InputFileLoc: &tg.InputPhotoFileLocation{
				ID:            42,
				AccessHash:    99,
				FileReference: []byte("ref"),
				ThumbSize:     "y",
			},
			Name: "photo.jpg",
			Size: 10,
			DC:   4,
			Date: 123,
		},
	}
	require.NoError(t, original.Add(context.Background(), task))

	restoredStore := newTaskStore(kvd)
	restored, ok, err := restoredStore.Get(context.Background(), task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task.ID, restored.ID)
	require.Equal(t, task.FileName, restored.FileName)
	require.Equal(t, task.FileSize, restored.FileSize)

	loc, ok := restored.Media.InputFileLoc.(*tg.InputPhotoFileLocation)
	require.True(t, ok)
	require.Equal(t, int64(42), loc.ID)
	require.Equal(t, int64(99), loc.AccessHash)
	require.Equal(t, []byte("ref"), loc.FileReference)
	require.Equal(t, "y", loc.ThumbSize)
}

func TestDownloadHandlerSuccessAndRange(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, &poolHolder{}, nil, nil)

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
	require.NoError(t, proxy.tasks.Add(context.Background(), task))

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
	}, &poolHolder{}, nil, nil)

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
	}, &poolHolder{}, nil, nil)

	task := &downloadTask{
		ID:       "task-1",
		FileName: "file.bin",
		FileSize: 10,
		Media:    &tmedia.Media{Name: "file.bin", Size: 10},
	}
	require.NoError(t, proxy.tasks.Add(context.Background(), task))

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
	}, &poolHolder{}, nil, nil)

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
	require.NoError(t, proxy.tasks.Add(context.Background(), task))

	req := httptest.NewRequest(http.MethodHead, "/download/task-1", nil)
	rec := httptest.NewRecorder()
	proxy.handleDownload(rec, req)

	require.Equal(t, http.StatusOK, rec.Result().StatusCode)
	require.False(t, called)
	require.Equal(t, "10", rec.Result().Header.Get("Content-Length"))
}

func TestStreamTelegramMediaStartsAtRangeOffset(t *testing.T) {
	t.Parallel()

	payload := make([]byte, 524288+13)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	invoker := &recordingUploadInvoker{data: payload}
	client := tg.NewClient(invoker)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{},
		Size:         int64(len(payload)),
	}

	var out bytes.Buffer
	err := streamTelegramMedia(context.Background(), client, media, 524288, int64(len(payload)-1), &out)
	require.NoError(t, err)
	require.Equal(t, payload[524288:], out.Bytes())
	require.NotEmpty(t, invoker.offsets)
	require.Equal(t, int64(524288), invoker.offsets[0])
}

type recordingUploadInvoker struct {
	data    []byte
	offsets []int64
	limits  []int
}

func (i *recordingUploadInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.UploadGetFileRequest)
	if !ok {
		return fmt.Errorf("unexpected request type %T", input)
	}
	box, ok := output.(*tg.UploadFileBox)
	if !ok {
		return fmt.Errorf("unexpected response type %T", output)
	}

	i.offsets = append(i.offsets, req.Offset)
	i.limits = append(i.limits, req.Limit)

	start := int(req.Offset)
	if start >= len(i.data) {
		box.File = &tg.UploadFile{
			Type:  &tg.StorageFileUnknown{},
			Bytes: nil,
		}
		return nil
	}

	end := start + req.Limit
	if end > len(i.data) {
		end = len(i.data)
	}

	box.File = &tg.UploadFile{
		Type:  &tg.StorageFileUnknown{},
		Bytes: i.data[start:end],
	}
	return nil
}

type memoryTaskStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryTaskStorage() *memoryTaskStorage {
	return &memoryTaskStorage{
		data: map[string][]byte{},
	}
}

func (m *memoryTaskStorage) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	value, ok := m.data[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m *memoryTaskStorage) Set(ctx context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memoryTaskStorage) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.data, key)
	return nil
}
