package httpdl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

// Exercise the complete HTTP -> Range -> scheduler -> upload.getFile path.
// Hold the first RPCs until every excess HTTP request has joined the queue, so
// the concurrency assertions do not depend on network or goroutine timing.
func TestHTTPConcurrentRangesRespectConfiguredDCBudgets(t *testing.T) {
	for _, capacity := range []int{1, 3, 8} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			const requestsPerDC = 32
			payload := make([]byte, 2*downloadStreamPartSize+123)
			for i := range payload {
				payload[i] = byte(i % 251)
			}
			gate := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(gate) }) }
			invokers := map[int]*gatedDownloadInvoker{}
			pool := perDCTestPool{clients: map[int]*tg.Client{}}
			for _, dc := range []int{2, 4} {
				invoker := &gatedDownloadInvoker{recordingUploadInvoker: recordingUploadInvoker{data: payload}, gate: gate}
				invokers[dc] = invoker
				pool.clients[dc] = tg.NewClient(invoker)
			}
			pools := &poolHolder{}
			pools.Set(pool)
			proxy := newDownloadProxy(config.HTTPConfig{}, 3, capacity, pools, nil, nil)
			for i, dc := range []int{2, 2, 4} {
				task := &downloadTask{
					ID: fmt.Sprintf("file-%d", i), FileName: testFileName, FileSize: int64(len(payload)), LastActiveAt: time.Now(),
					Media: &tmedia.Media{DC: dc, Size: int64(len(payload)), InputFileLoc: &tg.InputDocumentFileLocation{ID: int64(i + 1)}},
				}
				require.NoError(t, proxy.tasks.Add(context.Background(), task))
			}
			server := httptest.NewServer(proxy.routes())
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			defer unblock()
			results := make(chan error, 2*requestsPerDC)
			for _, dc := range []int{2, 4} {
				for n := range requestsPerDC {
					// Include unaligned starts, cross-fragment ranges and the short EOF.
					start, end := int64(n*7919+17), int64(n*7919+2067)
					if n%3 == 0 {
						start, end = downloadStreamPartSize-17, downloadStreamPartSize+1031
					} else if n%3 == 1 {
						start, end = int64(len(payload)-57), int64(len(payload)-1)
					}
					file := 2
					if dc == 2 {
						file = n % 2
					}
					go func() {
						results <- checkHTTPRange(ctx, server.Client(), fmt.Sprintf("%s/download/file-%d", server.URL, file), payload, start, end)
					}()
				}
			}
			require.Eventually(t, func() bool {
				snapshots := proxy.scheduler.Snapshots()
				if len(snapshots) != 2 {
					return false
				}
				for _, snapshot := range snapshots {
					if snapshot.ActiveChunks != capacity || snapshot.QueuedRequests != requestsPerDC-capacity || int(invokers[snapshot.DC].active.Load()) != capacity {
						return false
					}
				}
				return true
			}, 5*time.Second, time.Millisecond)
			unblock()
			for range 2 * requestsPerDC {
				require.NoError(t, <-results)
			}
			for _, invoker := range invokers {
				require.EqualValues(t, capacity, invoker.peak.Load())
				require.True(t, invoker.allRequestsStayWithinTelegramFragment())
			}
			require.Eventually(t, func() bool {
				for _, snapshot := range proxy.scheduler.Snapshots() {
					if snapshot.ActiveChunks != 0 || snapshot.QueuedRequests != 0 {
						return false
					}
				}
				return true
			}, time.Second, time.Millisecond)
		})
	}
}

func TestHTTPConcurrentLocalRangesBypassBusyTelegram(t *testing.T) {
	proxy, _ := completedLocalProxy(t) // No Telegram connection.
	lease, err := proxy.scheduler.Acquire(context.Background(), "busy", 2)
	require.NoError(t, err)
	defer lease.Release()
	chunk, err := lease.AcquireChunk(context.Background())
	require.NoError(t, err)
	defer chunk.Release() // Occupy both the only file slot and the only DC slot.
	server := httptest.NewServer(proxy.routes())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const requests = 32
	results := make(chan error, requests)
	for n := range requests {
		go func() {
			start := int64(n % 7)
			results <- checkHTTPRange(ctx, server.Client(), server.URL+"/download/"+testTaskID, []byte(localRangePayload), start, start+2)
		}()
	}
	for range requests {
		require.NoError(t, <-results)
	}
	snapshots := proxy.scheduler.Snapshots()
	require.Len(t, snapshots, 1)
	require.Equal(t, 1, snapshots[0].ActiveChunks)
	require.Zero(t, snapshots[0].QueuedRequests)
}

func checkHTTPRange(ctx context.Context, client *http.Client, url string, payload []byte, start, end int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	wantRange := fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload))
	if response.StatusCode != http.StatusPartialContent || response.ContentLength != end-start+1 || response.Header.Get("Content-Range") != wantRange || !bytes.Equal(payload[start:end+1], body) {
		return fmt.Errorf("incorrect response for %s: status=%d length=%d range=%q body length=%d", request.Header.Get("Range"), response.StatusCode, response.ContentLength, response.Header.Get("Content-Range"), len(body))
	}
	return nil
}

type perDCTestPool struct {
	testDownloadPool
	clients map[int]*tg.Client
}

func (p perDCTestPool) Client(_ context.Context, dc int) *tg.Client { return p.clients[dc] }

type gatedDownloadInvoker struct {
	recordingUploadInvoker
	gate         <-chan struct{}
	active, peak atomic.Int32
}

func (i *gatedDownloadInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.UploadGetFileRequest)
	if !ok || !req.GetPrecise() || req.Offset < 0 || req.Offset%1024 != 0 || req.Limit <= 0 || req.Limit%1024 != 0 || req.Limit > 1<<20 || req.Offset>>20 != (req.Offset+int64(req.Limit)-1)>>20 {
		return fmt.Errorf("invalid Telegram chunk request: %T", input)
	}
	active := i.active.Add(1)
	defer i.active.Add(-1)
	for peak := i.peak.Load(); active > peak; peak = i.peak.Load() {
		if i.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-i.gate:
		return i.recordingUploadInvoker.Invoke(ctx, input, output)
	}
}
