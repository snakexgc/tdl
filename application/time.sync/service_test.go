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

const preferredServer = "preferred.test"

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
		{intervalField: 0}, {intervalField: 59}, {intervalField: 86401}, {timeoutField: 0},
	} {
		_, err := config.New(Manifest().Config, values)
		require.Error(t, err)
	}
}

func TestClockKeepsLastSuccessfulOffsetAndRecovers(t *testing.T) {
	var available atomic.Bool
	var offset atomic.Int64
	s := New(probeFunc(func(context.Context, string, time.Duration) (types.TimeSample, error) {
		if !available.Load() {
			return types.TimeSample{}, errors.New("offline")
		}
		return types.TimeSample{Offset: time.Duration(offset.Load())}, nil
	}))
	configure(t, s, map[string]any{serverField: preferredServer})
	require.Equal(t, 5*time.Minute, s.configuration.Load().interval)
	require.Error(t, s.synchronize(context.Background()))
	require.False(t, s.Status().Synchronized)
	require.WithinDuration(t, time.Now(), s.Now(), time.Second)
	available.Store(true)
	offset.Store(int64(time.Hour))
	require.NoError(t, s.synchronize(context.Background()))
	first := s.Status()
	require.True(t, first.Synchronized)
	require.Equal(t, preferredServer, first.Server)
	require.WithinDuration(t, time.Now().Add(time.Hour), s.Now(), time.Second)
	available.Store(false)
	require.Error(t, s.synchronize(context.Background()))
	require.NotEmpty(t, s.Status().LastError)
	require.Equal(t, first.LastSuccess, s.Status().LastSuccess)
	require.Equal(t, time.Hour, s.Status().Offset)
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
	s := New(probeFunc(func(ctx context.Context, _ string, _ time.Duration) (types.TimeSample, error) {
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
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {serverField: preferredServer}})
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

func TestSelectionPrefersConfiguredAndFallsBackConcurrently(t *testing.T) {
	for _, configuredWorks := range []bool{true, false} {
		t.Run(map[bool]string{true: "configured", false: "fallback"}[configuredWorks], func(t *testing.T) {
			var calls atomic.Int32
			var wg sync.WaitGroup
			wg.Add(2)
			allStarted := make(chan struct{})
			go func() { wg.Wait(); close(allStarted) }()
			if configuredWorks {
				wg.Done()
				wg.Done()
			}
			probe := probeFunc(func(ctx context.Context, host string, _ time.Duration) (types.TimeSample, error) {
				if host == preferredServer {
					calls.Add(1)
					if configuredWorks {
						return types.TimeSample{Offset: time.Hour}, nil
					}
					return types.TimeSample{}, errors.New("offline")
				}
				wg.Done()
				select {
				case <-allStarted:
				case <-ctx.Done():
					return types.TimeSample{}, ctx.Err()
				}
				if host == "fast.test" {
					return types.TimeSample{Offset: -time.Hour, Elapsed: time.Millisecond}, nil
				}
				return types.TimeSample{Elapsed: time.Second}, nil
			})
			host, sample, err := selectServer(context.Background(), preferredServer, []string{"slow.test", "fast.test"}, probe, time.Second)
			require.NoError(t, err)
			if configuredWorks {
				require.Equal(t, preferredServer, host)
				require.Equal(t, time.Hour, sample.Offset)
				require.EqualValues(t, 1, calls.Load())
			} else {
				require.Equal(t, "fast.test", host)
				require.Equal(t, -time.Hour, sample.Offset)
				require.EqualValues(t, 3, calls.Load())
			}
		})
	}
}
