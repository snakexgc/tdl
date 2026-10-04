package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/schedule"
)

type taskClock struct{ at time.Time }

func (c taskClock) Now() time.Time { return c.at }
func (taskClock) Status() types.ClockStatus {
	return types.ClockStatus{Synchronized: true, Source: types.ClockSourceNTP}
}

func TestTaskRepositoriesShareLateBoundRuntimeClock(t *testing.T) {
	binding := new(rte.ClockBinding)
	ctx := rte.WithClock(context.Background(), binding)
	store := &storage.Memory{}
	local := taskhub.NewLocalRepository(store)
	forward := taskhub.NewForwardRepository(store)
	remote := taskhub.NewAria2Repository(store, time.Hour)
	first := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	binding.Bind(taskClock{first})
	require.Equal(t, first, ports.Now(ctx), "BSW and RTE must read the same context port")
	_, err := local.Create(ctx, types.LocalDownloadRecord{ID: "local", Status: types.LocalDownloadStatusQueued})
	require.NoError(t, err)
	require.NoError(t, forward.Save(ctx, types.ForwardJob{ID: "forward", Status: types.StatusQueued}))
	require.NoError(t, remote.Add(ctx, types.Aria2TaskRecord{GID: "remote", TaskID: "link"}))
	second := first.Add(time.Minute)
	binding.Bind(taskClock{second})
	_, err = local.Update(ctx, "local", func(record *types.LocalDownloadRecord) bool { record.Completed++; return true })
	require.NoError(t, err)
	_, err = forward.Update(ctx, "forward", func(job *types.ForwardJob) bool { job.Done++; return true })
	require.NoError(t, err)
	localRecord, ok, err := local.Get(ctx, "local")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first, localRecord.CreatedAt)
	require.Equal(t, second, localRecord.UpdatedAt)
	forwardRecord, ok, err := forward.Get(ctx, "forward")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first, forwardRecord.CreatedAt)
	require.Equal(t, second, forwardRecord.UpdatedAt)
	remoteRecords, err := remote.Records(ctx)
	require.NoError(t, err)
	require.Equal(t, first, remoteRecords["remote"].CreatedAt, "retention must use the same clock as creation")
	// Control fences use real deadlines even if application time is years ahead.
	lease, reserved, err := remote.ReserveControl(ctx, remoteRecords["remote"], time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, reserved)
	reservedAgain, ok, err := remote.ReserveControl(ctx, lease, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.False(t, ok, "clock correction must not expire an active RPC fence")
	require.True(t, lease.ControlUntil.Equal(reservedAgain.ControlUntil))
	observer := taskhub.Aria2Observations{Links: taskhub.LinkRepository{Store: store}}
	observation := lease
	observation.Status = string(types.DownloadActive)
	applied, err := observer.Apply(ctx, ports.ObservedLink{}, observation, false, second, time.Hour)
	require.NoError(t, err)
	require.False(t, applied, "an active control fence must reject observations despite the application offset")
	require.NoError(t, observer.Cleanup(ctx, second, time.Hour))
	remoteRecords, err = remote.Records(ctx)
	require.NoError(t, err)
	require.Contains(t, remoteRecords, "remote", "cleanup must retain an active control lease")
	binding.Bind(nil)
	require.Equal(t, types.ClockSourceSystem, ports.ClockFrom(ctx).Status().Source)
	require.WithinDuration(t, time.Now(), ports.Now(ctx), time.Second)
}

func TestRuntimeStateAndDiagnosticsUseClockWithoutChangingOverrunTimers(t *testing.T) {
	binding := new(rte.ClockBinding)
	ctx := rte.WithClock(context.Background(), binding)
	first := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	binding.Bind(taskClock{first})
	group := schedule.New(ctx)
	t.Cleanup(func() { require.NoError(t, group.Stop(context.Background())) })
	require.NoError(t, group.Run("clock-overrun", 0, time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, nil))
	require.Eventually(t, func() bool {
		states := group.Statuses()
		return len(states) == 1 && states[0].Overdue
	}, time.Second, time.Millisecond, "overrun detection must use elapsed time despite a future application timestamp")
	require.Equal(t, first, group.Statuses()[0].StartedAt)
	process := rte.NewProcess(ctx, types.DefaultAccount, "clock-process")
	release := make(chan struct{})
	t.Cleanup(func() { require.NoError(t, process.Stop(context.Background())) })
	started, err := process.Start(func(ctx context.Context) error {
		select {
		case <-release:
			return errors.New("observable failure")
		case <-ctx.Done():
			return nil
		}
	}, rte.Recovery{})
	require.NoError(t, err)
	require.True(t, started)
	require.Equal(t, first, process.Health().Components[0].Runnables[0].StartedAt)
	second := first.Add(time.Minute)
	binding.Bind(taskClock{second})
	close(release)
	require.Eventually(t, func() bool { return !process.Running() }, time.Second, time.Millisecond)
	health := process.Health()
	require.Equal(t, second, health.Components[0].Runnables[0].FinishedAt)
	require.Len(t, health.Events, 1)
	require.Equal(t, second, health.Events[0].At)
	require.NoError(t, group.Stop(context.Background()))
	require.Equal(t, second, group.Statuses()[0].FinishedAt)
	require.False(t, group.Statuses()[0].Overdue)
}
