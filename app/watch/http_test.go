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
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/dcpool"
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
	}, 2, 4, &poolHolder{}, nil, nil)
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

	peer := &tg.InputPeerChannel{ChannelID: 1, AccessHash: 2}
	first, err := proxy.NewTask(context.Background(), 1, 2, peer, "first.bin", 10, media)
	require.NoError(t, err)
	second, err := proxy.NewTask(context.Background(), 3, 4, peer, "second.bin", 10, media)
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
		Peer:      &tg.InputPeerChannel{ChannelID: 100, AccessHash: 101},
		FileName:  "photo.jpg",
		FileSize:  10,
		CreatedAt: time.Now(),
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

	peer, ok := restored.Peer.(*tg.InputPeerChannel)
	require.True(t, ok)
	require.Equal(t, int64(100), peer.ChannelID)
	require.Equal(t, int64(101), peer.AccessHash)

	loc, ok := restored.Media.InputFileLoc.(*tg.InputPhotoFileLocation)
	require.True(t, ok)
	require.Equal(t, int64(42), loc.ID)
	require.Equal(t, int64(99), loc.AccessHash)
	require.Equal(t, []byte("ref"), loc.FileReference)
	require.Equal(t, "y", loc.ThumbSize)
}

func TestTaskStoreExpiresPersistentTask(t *testing.T) {
	t.Parallel()

	kvd := newMemoryTaskStorage()
	original := newTaskStore(kvd)
	task := &downloadTask{
		ID:        "document_42",
		PeerID:    100,
		MessageID: 200,
		Peer:      &tg.InputPeerChannel{ChannelID: 100, AccessHash: 101},
		FileName:  "file.bin",
		FileSize:  10,
		CreatedAt: time.Now().Add(-downloadTaskTTL - time.Second),
		Media: &tmedia.Media{
			InputFileLoc: &tg.InputDocumentFileLocation{
				ID:            42,
				AccessHash:    99,
				FileReference: []byte("ref"),
			},
			Name: "file.bin",
			Size: 10,
			DC:   4,
			Date: 123,
		},
	}
	require.NoError(t, original.Add(context.Background(), task))

	restoredStore := newTaskStore(kvd)
	restored, ok, err := restoredStore.Get(context.Background(), task.ID)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, restored)

	_, err = kvd.Get(context.Background(), downloadTaskStorageKey(task.ID))
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestDownloadHandlerSuccessAndRange(t *testing.T) {
	t.Parallel()

	proxy := newDownloadProxy(config.HTTPConfig{
		Listen:        "127.0.0.1:0",
		PublicBaseURL: "http://127.0.0.1:8080",
	}, 2, 4, &poolHolder{}, nil, nil)

	payload := []byte("0123456789")
	proxy.stream = func(ctx context.Context, task *downloadTask, lease *downloadLease, start, end int64, w io.Writer) error {
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
	}, 2, 4, &poolHolder{}, nil, nil)

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
	}, 2, 4, &poolHolder{}, nil, nil)

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
	}, 2, 4, &poolHolder{}, nil, nil)

	called := false
	proxy.stream = func(ctx context.Context, task *downloadTask, lease *downloadLease, start, end int64, w io.Writer) error {
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

func TestValidateWatchConfigRejectsInvalidBufferMode(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.HTTP.PublicBaseURL = "http://127.0.0.1:8080"
	cfg.HTTP.Buffer.Mode = "disk"

	err := validateWatchConfig(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "http.buffer.mode")
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
		DC:           2,
	}
	pool := testDownloadPool{client: client}
	lease := mustAcquireDownloadLease(t, 4)
	defer lease.Release()

	var out bytes.Buffer
	err := streamTelegramMedia(context.Background(), pool, &telegramMediaSource{media: media}, lease, 524288, int64(len(payload)-1), &out)
	require.NoError(t, err)
	require.Equal(t, payload[524288:], out.Bytes())
	require.NotEmpty(t, invoker.offsets)
	require.Equal(t, int64(524288), invoker.offsets[0])
	require.Equal(t, telegramGetFilePreciseAlignment, invoker.limits[0])
}

func TestStreamTelegramMediaAlignsFinalLimit(t *testing.T) {
	t.Parallel()

	payload := make([]byte, 186100)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	invoker := &recordingUploadInvoker{data: payload}
	client := tg.NewClient(invoker)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{},
		Size:         int64(len(payload)),
		DC:           2,
	}
	pool := testDownloadPool{client: client}
	lease := mustAcquireDownloadLease(t, 4)
	defer lease.Release()

	var out bytes.Buffer
	err := streamTelegramMedia(context.Background(), pool, &telegramMediaSource{media: media}, lease, 0, int64(len(payload)-1), &out)
	require.NoError(t, err)
	require.Equal(t, payload, out.Bytes())
	require.Equal(t, []int64{0}, invoker.offsets)
	require.Equal(t, []int{186368}, invoker.limits)
	require.Zero(t, invoker.limits[0]%telegramGetFilePreciseAlignment)
}

func TestStreamTelegramMediaSplitsRangeByTelegramFragment(t *testing.T) {
	t.Parallel()

	payload := make([]byte, downloadStreamPartSize*2+3000)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	invoker := &recordingUploadInvoker{data: payload}
	client := tg.NewClient(invoker)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{},
		Size:         int64(len(payload)),
		DC:           2,
	}
	pool := testDownloadPool{client: client}
	lease := mustAcquireDownloadLease(t, 4)
	defer lease.Release()

	start := int64(100)
	end := int64(downloadStreamPartSize) + 1500

	var out bytes.Buffer
	err := streamTelegramMedia(context.Background(), pool, &telegramMediaSource{media: media}, lease, start, end, &out)
	require.NoError(t, err)
	require.Equal(t, payload[start:end+1], out.Bytes())
	require.Equal(t, []int64{0, downloadStreamPartSize}, invoker.offsets)
	require.Equal(t, []int{downloadStreamPartSize, 2048}, invoker.limits)
	require.True(t, invoker.allRequestsStayWithinTelegramFragment())
}

func TestStreamTelegramMediaParallelUsesMultipleWorkers(t *testing.T) {
	t.Parallel()

	payload := make([]byte, downloadStreamPartSize*3)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	invoker := &recordingUploadInvoker{
		data:  payload,
		delay: 50 * time.Millisecond,
	}
	client := tg.NewClient(invoker)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{},
		Size:         int64(len(payload)),
		DC:           2,
	}
	pool := testDownloadPool{client: client}
	lease := mustAcquireDownloadLease(t, 4)
	defer lease.Release()

	var out bytes.Buffer
	err := streamTelegramMedia(context.Background(), pool, &telegramMediaSource{media: media}, lease, 0, int64(len(payload)-1), &out)
	require.NoError(t, err)
	require.Equal(t, payload, out.Bytes())
	require.GreaterOrEqual(t, invoker.maxConcurrent(), 2)
}

func TestStreamTelegramMediaMemoryBufferPrefetchesWhileWriterBlocks(t *testing.T) {
	t.Parallel()

	const bufferSlots = 8
	payload := make([]byte, downloadStreamPartSize*12)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	invoker := &recordingUploadInvoker{data: payload}
	client := tg.NewClient(invoker)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{},
		Size:         int64(len(payload)),
		DC:           2,
	}
	pool := testDownloadPool{client: client}
	lease, err := newDownloadLimiter(1, 2, bufferSlots).Acquire(context.Background(), "task-1")
	require.NoError(t, err)
	defer lease.Release()

	out := newBlockingFirstWriteBuffer()
	defer out.Unblock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- streamTelegramMedia(context.Background(), pool, &telegramMediaSource{media: media}, lease, 0, int64(len(payload)-1), out)
	}()

	select {
	case <-out.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first write")
	}

	require.Eventually(t, func() bool {
		return invoker.totalCalls() >= bufferSlots-1
	}, time.Second, 10*time.Millisecond)

	out.Unblock()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream completion")
	}
	require.Equal(t, payload, out.Bytes())
}

func TestStreamTelegramMediaRetriesTimeoutChunk(t *testing.T) {
	t.Parallel()

	payload := make([]byte, downloadStreamPartSize+128)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	invoker := &recordingUploadInvoker{
		data: payload,
		failures: map[int64]int{
			0: 1,
		},
		failErr: tgerr.New(500, tg.ErrTimeout),
	}
	client := tg.NewClient(invoker)
	media := &tmedia.Media{
		InputFileLoc: &tg.InputDocumentFileLocation{},
		Size:         int64(len(payload)),
		DC:           2,
	}
	pool := testDownloadPool{client: client}
	lease := mustAcquireDownloadLease(t, 2)
	defer lease.Release()

	var out bytes.Buffer
	err := streamTelegramMedia(context.Background(), pool, &telegramMediaSource{media: media}, lease, 0, int64(len(payload)-1), &out)
	require.NoError(t, err)
	require.Equal(t, payload, out.Bytes())
	require.GreaterOrEqual(t, invoker.callCount(0), 2)
}

type recordingUploadInvoker struct {
	mu          sync.Mutex
	data        []byte
	offsets     []int64
	limits      []int
	delay       time.Duration
	inFlight    int
	maxInFlight int
	failures    map[int64]int
	failErr     error
	calls       map[int64]int
}

type blockingFirstWriteBuffer struct {
	bytes.Buffer

	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingFirstWriteBuffer() *blockingFirstWriteBuffer {
	return &blockingFirstWriteBuffer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingFirstWriteBuffer) Write(p []byte) (int, error) {
	b.startOnce.Do(func() {
		close(b.started)
		<-b.release
	})
	return b.Buffer.Write(p)
}

func (b *blockingFirstWriteBuffer) Unblock() {
	b.releaseOnce.Do(func() {
		close(b.release)
	})
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

	i.mu.Lock()
	i.offsets = append(i.offsets, req.Offset)
	i.limits = append(i.limits, req.Limit)
	i.inFlight++
	if i.inFlight > i.maxInFlight {
		i.maxInFlight = i.inFlight
	}
	if i.calls == nil {
		i.calls = map[int64]int{}
	}
	i.calls[req.Offset]++
	if remaining := i.failures[req.Offset]; remaining > 0 {
		i.failures[req.Offset] = remaining - 1
		i.inFlight--
		i.mu.Unlock()
		return i.failErr
	}
	delay := i.delay
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		i.inFlight--
		i.mu.Unlock()
	}()

	if delay > 0 {
		time.Sleep(delay)
	}

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

func (i *recordingUploadInvoker) maxConcurrent() int {
	i.mu.Lock()
	defer i.mu.Unlock()

	return i.maxInFlight
}

func (i *recordingUploadInvoker) callCount(offset int64) int {
	i.mu.Lock()
	defer i.mu.Unlock()

	return i.calls[offset]
}

func (i *recordingUploadInvoker) totalCalls() int {
	i.mu.Lock()
	defer i.mu.Unlock()

	return len(i.offsets)
}

func (i *recordingUploadInvoker) allRequestsStayWithinTelegramFragment() bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	for idx := range i.offsets {
		offset := i.offsets[idx]
		limit := i.limits[idx]
		if offset%telegramGetFilePreciseAlignment != 0 {
			return false
		}
		if limit%telegramGetFilePreciseAlignment != 0 {
			return false
		}
		if limit > telegramGetFileFragmentWindowSize {
			return false
		}
		if offset/int64(telegramGetFileFragmentWindowSize) != (offset+int64(limit)-1)/int64(telegramGetFileFragmentWindowSize) {
			return false
		}
	}

	return true
}

type testDownloadPool struct {
	client *tg.Client
}

func (p testDownloadPool) Client(ctx context.Context, dc int) *tg.Client {
	return p.client
}

func (p testDownloadPool) Takeout(ctx context.Context, dc int) *tg.Client {
	return p.client
}

func (p testDownloadPool) Default(ctx context.Context) *tg.Client {
	return p.client
}

func (p testDownloadPool) Close() error {
	return nil
}

var _ dcpool.Pool = testDownloadPool{}

func mustAcquireDownloadLease(t *testing.T, maxWorkers int) *downloadLease {
	t.Helper()

	lease, err := newDownloadLimiter(1, maxWorkers).Acquire(context.Background(), "task-1")
	require.NoError(t, err)
	return lease
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
