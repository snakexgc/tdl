package aria2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

func TestObserverReadsEveryWaitingAndStoppedPage(t *testing.T) {
	client := &fakeAria2ControlClient{
		waiting: make([]types.Aria2DownloadStatus, aria2ControlBatchSize+1),
		stopped: make([]types.Aria2DownloadStatus, aria2ControlBatchSize+1),
	}
	client.waiting[aria2ControlBatchSize] = types.Aria2DownloadStatus{GID: "last-waiting", Status: aria2StatusWaiting, Files: filesWithURI(testDownloadURL1)}
	client.stopped[aria2ControlBatchSize] = types.Aria2DownloadStatus{GID: "last-stopped", Status: aria2StatusComplete, Files: filesWithURI(testDownloadURL1)}
	repository := &linkObservation{}
	observer := Observer{Client: client, Repository: repository, PublicBaseURL: testControlBaseURL, TTL: time.Hour}
	statuses, err := observer.Observe(context.Background())
	require.NoError(t, err)
	require.Contains(t, statuses, "last-waiting")
	require.Contains(t, statuses, "last-stopped")
	require.Len(t, repository.applied, 2, "later tasks need retention and completion updates too")
}

type failedObservationPage struct{ fakeAria2ControlClient }

func (c *failedObservationPage) TellWaiting(ctx context.Context, offset, num int) ([]types.Aria2DownloadStatus, error) {
	if offset > 0 {
		return nil, errors.New("next page unavailable")
	}
	return c.fakeAria2ControlClient.TellWaiting(ctx, offset, num)
}

func TestObserverDoesNotExpireTasksAfterIncompletePagination(t *testing.T) {
	client := &failedObservationPage{fakeAria2ControlClient{waiting: make([]types.Aria2DownloadStatus, aria2ControlBatchSize)}}
	repository := &linkObservation{}
	observer := Observer{Client: client, Repository: repository, TTL: time.Hour}
	require.ErrorContains(t, observer.Sync(context.Background()), "next page unavailable")
	require.Empty(t, repository.ttls, "partial observations must not lead to TTL cleanup")
}
