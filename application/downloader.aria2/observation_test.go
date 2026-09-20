package aria2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/snakexgc/tdl/interfaces/ports"
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

const logObservedGID = "owned"

type logObservation struct {
	linkObservation
	previous string
	accept   bool
}

func (o *logObservation) Snapshot(context.Context) (ports.Aria2ObservationSnapshot, error) {
	return ports.Aria2ObservationSnapshot{Records: map[string]types.Aria2TaskRecord{
		logObservedGID: {GID: logObservedGID, TaskID: testDocument1, Status: o.previous},
	}}, nil
}

func (o *logObservation) Apply(context.Context, ports.ObservedLink, types.Aria2TaskRecord, bool, time.Time, time.Duration) (bool, error) {
	return o.accept, nil
}

func TestObserverLogsOnlyAcceptedStateChanges(t *testing.T) {
	for _, test := range []struct {
		name, previous, next string
		accept               bool
		count                int
		level                zapcore.Level
	}{
		{name: "complete", previous: aria2StatusActive, next: aria2StatusComplete, accept: true, count: 1, level: zap.InfoLevel},
		{name: "failed", previous: aria2StatusActive, next: aria2StatusError, accept: true, count: 1, level: zap.ErrorLevel},
		{name: "same state", previous: aria2StatusComplete, next: aria2StatusComplete, accept: true},
		{name: "stale snapshot", previous: aria2StatusActive, next: aria2StatusComplete, accept: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			client := &fakeAria2ControlClient{stopped: []types.Aria2DownloadStatus{{GID: logObservedGID, Status: test.next}}}
			o := Observer{Logger: zap.New(core), Client: client, Repository: &logObservation{previous: test.previous, accept: test.accept}}
			_, err := o.Observe(context.Background())
			require.NoError(t, err)
			require.Len(t, logs.All(), test.count)
			if test.count > 0 {
				require.Equal(t, test.level, logs.All()[0].Level)
				require.Equal(t, ID, logs.All()[0].ContextMap()["component"])
				require.Equal(t, testDocument1, logs.All()[0].ContextMap()["task_id"])
			}
		})
	}
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
