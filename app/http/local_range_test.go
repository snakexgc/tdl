package httpdl

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	transfer "github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

const localRangePayload = "0123456789"

func completedLocalProxy(t *testing.T) (*downloadProxy, types.LocalDownloadRecord) {
	t.Helper()
	kvd := newMemoryTaskStorage()
	writer := newDownloadProxy(config.HTTPConfig{}, 1, 1, nil, kvd, nil)
	now := time.Now()
	task := &downloadTask{
		ID: testTaskID, FileName: testFileName, FileSize: int64(len(localRangePayload)), CreatedAt: now, LastActiveAt: now,
		Media: &tmedia.Media{InputFileLoc: &tg.InputDocumentFileLocation{ID: 1, AccessHash: 2}, Name: testFileName, Size: int64(len(localRangePayload)), DC: 2},
	}
	require.NoError(t, writer.tasks.Add(context.Background(), task))
	path := filepath.Join(t.TempDir(), testFileName)
	require.NoError(t, os.WriteFile(path, []byte(localRangePayload), 0o600))
	record := types.LocalDownloadRecord{
		ID: task.ID, TaskID: task.ID, FileName: task.FileName, Path: path,
		Total: task.FileSize, Completed: task.FileSize, Status: types.LocalDownloadStatusComplete,
	}
	require.NoError(t, taskhub.NewLocalRepository(kvd).Save(context.Background(), record))
	// Reopen the persisted records as after a restart, without a Telegram pool.
	return newDownloadProxy(config.HTTPConfig{}, 1, 1, nil, kvd, nil), record
}

func TestDownloadLinkServesCompletedLocalFileWithoutTelegram(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, byteRange, ifRange, body string
		status                                 int
	}{
		{"full", http.MethodGet, "", "", localRangePayload, http.StatusOK},
		{"head", http.MethodHead, "", "", "", http.StatusOK},
		{"range", http.MethodGet, "bytes=2-5", "", "2345", http.StatusPartialContent},
		{"suffix", http.MethodGet, "bytes=-3", "", "789", http.StatusPartialContent},
		{"stale_etag", http.MethodGet, "bytes=2-5", `"stale"`, localRangePayload, http.StatusOK},
		{"multipart", http.MethodGet, "bytes=0-1,8-9", "", "", http.StatusPartialContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, _ := completedLocalProxy(t)
			// Local HTTP delivery must also work when all Telegram file slots are busy.
			busy, err := proxy.scheduler.Acquire(context.Background(), "another-task", 2)
			require.NoError(t, err)
			defer busy.Release()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			request := httptest.NewRequestWithContext(ctx, tc.method, "/download/"+testTaskID, nil)
			request.Header.Set("Range", tc.byteRange)
			request.Header.Set("If-Range", tc.ifRange)
			response := httptest.NewRecorder()
			proxy.handleDownload(response, request)
			require.Equal(t, tc.status, response.Code)
			require.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
			require.Equal(t, "bytes", response.Header().Get("Accept-Ranges"))
			if tc.name == "multipart" {
				_, params, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
				require.NoError(t, err)
				reader := multipart.NewReader(response.Body, params["boundary"])
				for _, want := range []string{"01", "89"} {
					part, err := reader.NextPart()
					require.NoError(t, err)
					body, err := io.ReadAll(part)
					require.NoError(t, err)
					require.Equal(t, want, string(body))
				}
				_, err = reader.NextPart()
				require.ErrorIs(t, err, io.EOF)
			} else {
				require.Equal(t, tc.body, response.Body.String())
			}
			data, err := proxy.tasks.kv.Get(context.Background(), downloadTaskStorageKey(testTaskID))
			require.NoError(t, err)
			status, err := ParseDownloadTaskHTTPStatus(data)
			require.NoError(t, err)
			require.Equal(t, tc.method == http.MethodGet && tc.status == http.StatusOK, status.Completed)
			if tc.method == http.MethodHead {
				require.Zero(t, status.DeliveredBytes)
			}
		})
	}
}

func TestDownloadLinkFallsBackToTelegramWhenLocalFileIsUnavailable(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"unfinished", "missing", "wrong_size", "wrong_source", "directory", "relative_path"} {
		t.Run(reason, func(t *testing.T) {
			proxy, record := completedLocalProxy(t)
			switch reason {
			case "unfinished":
				record.Status = types.LocalDownloadStatusPaused
			case "missing":
				require.NoError(t, os.Remove(record.Path))
			case "wrong_size":
				require.NoError(t, os.Truncate(record.Path, record.Total-1))
			case "wrong_source":
				record.TaskID = "another-source"
			case "directory":
				record.Path = t.TempDir()
			case "relative_path":
				record.Path = testFileName
			}
			require.NoError(t, taskhub.NewLocalRepository(proxy.tasks.kv).Save(context.Background(), record))
			proxy.pools.Set(testDownloadPool{})
			const remotePayload = "abcdefghij"
			proxy.stream = func(_ context.Context, _ *downloadTask, _ *transfer.TaskLease, start, end int64, w io.Writer) error {
				_, err := io.WriteString(w, remotePayload[start:end+1])
				return err
			}
			response := httptest.NewRecorder()
			proxy.handleDownload(response, httptest.NewRequest(http.MethodGet, "/download/"+testTaskID, nil))
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, remotePayload, response.Body.String())
		})
	}
}

func TestLocalRangeCancellationAndRelease(t *testing.T) {
	t.Parallel()
	proxy, _ := completedLocalProxy(t)
	_, transport, found, err := (rangeSource{proxy: proxy}).Open(context.Background(), testTaskID)
	require.NoError(t, err)
	require.True(t, found)
	local := transport.(*localFileTransfer)
	t.Cleanup(func() { require.NoError(t, local.Close()) })
	lease, err := local.Acquire(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buffer bytes.Buffer
	require.ErrorIs(t, local.Stream(ctx, lease, 0, 9, &buffer), context.Canceled)
	require.Empty(t, buffer.Bytes())
	lease.Release()
	_, err = local.file.Read(make([]byte, 1))
	require.ErrorIs(t, err, os.ErrClosed)
}
