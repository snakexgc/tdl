package timesync

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const sampleServer = "ntp.aliyun.com"

type probeFunc func(context.Context, string, time.Duration) (types.TimeSample, error)

func (f probeFunc) Query(ctx context.Context, host string, timeout time.Duration) (types.TimeSample, error) {
	return f(ctx, host, timeout)
}

func configure(t *testing.T, s *Service, values map[string]any) {
	t.Helper()
	view, err := config.New(Manifest().Config, values)
	require.NoError(t, err)
	require.NoError(t, s.Reconfigure(context.Background(), view))
}

func TestConfigurationSupportsMinutesAndRejectsInvalidPeriods(t *testing.T) {
	for _, seconds := range []int{60, 180, 300} {
		s := New(nil)
		configure(t, s, map[string]any{intervalField: seconds})
		require.Equal(t, time.Duration(seconds)*time.Second, s.configuration.Load().interval)
	}
	for _, values := range []map[string]any{
		{intervalField: 0}, {intervalField: 59}, {intervalField: 86401}, {timeoutField: 0}, {"server": "retired.test"},
	} {
		_, err := config.New(Manifest().Config, values)
		require.Error(t, err)
	}
}

func TestClockFallsBackToSystemAndRecovers(t *testing.T) {
	var available atomic.Bool
	var offset atomic.Int64
	s := New(probeFunc(func(_ context.Context, host string, _ time.Duration) (types.TimeSample, error) {
		if !available.Load() || host != sampleServer {
			return types.TimeSample{}, errors.New("offline")
		}
		return types.TimeSample{Offset: time.Duration(offset.Load())}, nil
	}))
	configure(t, s, nil)
	require.Equal(t, 5*time.Minute, s.configuration.Load().interval)
	require.Error(t, s.synchronize(context.Background()))
	require.False(t, s.Status().Synchronized)
	require.Equal(t, types.ClockSourceSystem, s.Status().Source)
	require.WithinDuration(t, time.Now(), s.Now(), time.Second)
	available.Store(true)
	offset.Store(int64(time.Hour))
	require.NoError(t, s.synchronize(context.Background()))
	first := s.Status()
	require.True(t, first.Synchronized)
	require.Equal(t, sampleServer, first.Server)
	require.WithinDuration(t, time.Now().Add(time.Hour), s.Now(), time.Second)
	available.Store(false)
	require.Error(t, s.synchronize(context.Background()))
	require.NotEmpty(t, s.Status().LastError)
	require.Equal(t, first.LastSuccess, s.Status().LastSuccess)
	require.Zero(t, s.Status().Offset)
	require.Empty(t, s.Status().Server)
	require.False(t, s.Status().Synchronized)
	require.Equal(t, types.ClockSourceSystem, s.Status().Source)
	require.WithinDuration(t, time.Now(), s.Now(), time.Second)
	available.Store(true)
	offset.Store(int64(-time.Hour))
	require.NoError(t, s.synchronize(context.Background()))
	require.Empty(t, s.Status().LastError)
	require.WithinDuration(t, time.Now().Add(-time.Hour), s.Now(), time.Second)
}

// Shorten only the test scheduler period; production schema remains >= 60 s.
type quickService struct{ *Service }

func (s quickService) Start(ctx context.Context) error {
	next := *s.configuration.Load()
	next.interval = 10 * time.Millisecond
	s.configuration.Store(&next)
	return s.Service.Start(ctx)
}

func TestPeriodicSynchronizationNeverBlocksClockReadersOrOverlaps(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var calls, active, maximum atomic.Int32
	s := New(probeFunc(func(ctx context.Context, host string, _ time.Duration) (types.TimeSample, error) {
		if host != sampleServer {
			return types.TimeSample{}, errors.New("offline")
		}
		calls.Add(1)
		count := active.Add(1)
		defer active.Add(-1)
		if count > maximum.Load() {
			maximum.Store(count)
		}
		entered <- struct{}{}
		select {
		case <-release:
			return types.TimeSample{Offset: time.Minute}, nil
		case <-ctx.Done():
			return types.TimeSample{}, ctx.Err()
		}
	}))
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(Manifest(), func() rte.Component { return quickService{s} }))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	<-entered
	port, err := host.Resolve(ports.ClockName)
	require.NoError(t, err)
	clock := port.(ports.Clock)
	read := make(chan time.Time, 1)
	go func() { read <- clock.Now() }()
	select {
	case now := <-read:
		require.WithinDuration(t, time.Now(), now, time.Second)
	case <-time.After(time.Second):
		t.Fatal("time port waited for network synchronization")
	}
	// Keep the first round stalled across several periods: no duplicate query.
	time.Sleep(40 * time.Millisecond)
	require.EqualValues(t, 1, calls.Load())
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("periodic synchronization did not run again")
	}
	require.WithinDuration(t, time.Now().Add(time.Minute), clock.Now(), time.Second)
	bounded, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, host.Stop(bounded))
	require.EqualValues(t, 0, active.Load())
	require.EqualValues(t, 1, maximum.Load())
	require.EqualValues(t, 2, calls.Load())
}

func TestSelectionUsesFirstValidResponseAndCancelsTheRest(t *testing.T) {
	var started sync.WaitGroup
	started.Add(4)
	allStarted := make(chan struct{})
	go func() { started.Wait(); close(allStarted) }()
	var cancelled atomic.Int32
	probe := probeFunc(func(ctx context.Context, host string, _ time.Duration) (types.TimeSample, error) {
		started.Done()
		select {
		case <-allStarted:
		case <-ctx.Done():
			return types.TimeSample{}, ctx.Err()
		}
		if host == "invalid.test" {
			return types.TimeSample{}, errors.New("invalid response")
		}
		if host == "first.test" {
			// Selection follows arrival, irrespective of a reported RTT.
			return types.TimeSample{Offset: -time.Hour, Elapsed: time.Second}, nil
		}
		<-ctx.Done()
		cancelled.Add(1)
		return types.TimeSample{Elapsed: time.Nanosecond}, ctx.Err()
	})
	begin := time.Now()
	host, sample, err := selectServer(context.Background(), []string{"slow.test", "other-slow.test", "invalid.test", "first.test"}, probe, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "first.test", host)
	require.Equal(t, -time.Hour, sample.Offset)
	require.EqualValues(t, 2, cancelled.Load(), "losers must be drained before the round ends")
	require.Less(t, time.Since(begin), time.Second, "winner must not wait for slow servers to time out")
}

func TestSelectionBoundsOneRoundAndDeduplicatesServers(t *testing.T) {
	var calls atomic.Int32
	probe := probeFunc(func(ctx context.Context, _ string, _ time.Duration) (types.TimeSample, error) {
		calls.Add(1)
		<-ctx.Done()
		return types.TimeSample{}, ctx.Err()
	})
	begin := time.Now()
	_, _, err := selectServer(context.Background(), []string{sampleServer, "NTP.ALIYUN.COM", "other.test", ""}, probe, 30*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.EqualValues(t, 2, calls.Load())
	require.Less(t, time.Since(begin), time.Second)
}

func TestSelectionToleratesPanicsAndCancelledRoundsDoNotChangeClock(t *testing.T) {
	_, _, err := selectServer(context.Background(), []string{"panic.test"}, probeFunc(func(context.Context, string, time.Duration) (types.TimeSample, error) {
		panic("probe failed")
	}), time.Second)
	require.ErrorContains(t, err, "probe panic")
	s := New(probeFunc(func(context.Context, string, time.Duration) (types.TimeSample, error) {
		return types.TimeSample{Offset: time.Hour}, nil
	}))
	configure(t, s, nil)
	require.NoError(t, s.synchronize(context.Background()))
	previous := s.Status()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, s.synchronize(ctx))
	require.Equal(t, previous, s.Status())
}
