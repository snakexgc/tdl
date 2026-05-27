package aria2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	testGIDStalled = "stalled"
	testGIDRecover = "recover"
)

func TestZeroSpeedMonitorPausesAndResumesZeroSpeedTaskAfterThreshold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:       testGIDStalled,
		TaskID:    "document_stalled",
		CreatedAt: time.Now(),
	}))

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: testGIDStalled, Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "100", DownloadSpeed: "0"},
		},
	}
	monitor := newZeroSpeedMonitor(client, store, "http://127.0.0.1:8080", zap.NewNop(), zeroSpeedMonitorConfig{
		StallThreshold: 2 * time.Second,
		PauseDuration:  time.Millisecond,
		ActionTimeout:  time.Second,
	})

	current := time.Unix(100, 0)
	monitor.now = func() time.Time { return current }

	// First poll: zero speed observed, stall clock starts — no action yet.
	require.NoError(t, monitor.poll(ctx))
	require.Empty(t, client.forcePaused)

	// Still under threshold.
	current = current.Add(time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Empty(t, client.forcePaused)

	// Past threshold: pause/resume should fire.
	current = current.Add(2 * time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Equal(t, []string{testGIDStalled}, client.forcePaused)
	require.Equal(t, []string{testGIDStalled}, client.unpaused)
}

func TestZeroSpeedMonitorDoesNotActOnMovingTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:       "moving",
		TaskID:    "document_moving",
		CreatedAt: time.Now(),
	}))

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: "moving", Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "100", DownloadSpeed: "512000"},
		},
	}
	monitor := newZeroSpeedMonitor(client, store, "http://127.0.0.1:8080", zap.NewNop(), zeroSpeedMonitorConfig{
		StallThreshold: time.Millisecond,
		PauseDuration:  time.Millisecond,
		ActionTimeout:  time.Second,
	})

	current := time.Unix(100, 0)
	monitor.now = func() time.Time { return current }

	// Advance far past any threshold — still no action because speed > 0.
	current = current.Add(time.Hour)
	require.NoError(t, monitor.poll(ctx))
	require.Empty(t, client.forcePaused)
}

func TestZeroSpeedMonitorClearsStallWhenSpeedResumes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:       testGIDRecover,
		TaskID:    "document_recover",
		CreatedAt: time.Now(),
	}))

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: testGIDRecover, Status: aria2StatusActive, TotalLength: "1000", CompletedLength: "100", DownloadSpeed: "0"},
		},
	}
	monitor := newZeroSpeedMonitor(client, store, "http://127.0.0.1:8080", zap.NewNop(), zeroSpeedMonitorConfig{
		StallThreshold: 5 * time.Second,
		PauseDuration:  time.Millisecond,
		ActionTimeout:  time.Second,
	})

	current := time.Unix(100, 0)
	monitor.now = func() time.Time { return current }

	// Zero speed observed — stall clock starts.
	require.NoError(t, monitor.poll(ctx))

	// Speed recovers before threshold.
	client.active[0].DownloadSpeed = "256000"
	current = current.Add(3 * time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Empty(t, client.forcePaused)

	// Speed drops again; a new stall clock should start.
	client.active[0].DownloadSpeed = "0"
	current = current.Add(time.Second)
	require.NoError(t, monitor.poll(ctx))

	// Advance past threshold from the new stall start.
	current = current.Add(10 * time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Equal(t, []string{testGIDRecover}, client.forcePaused)
	require.Equal(t, []string{testGIDRecover}, client.unpaused)
}

func TestZeroSpeedMonitorIgnoresNonOwnedTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Store is empty — no registered GIDs, no known download prefix match.
	store := NewTaskStore(newMemoryTaskStorage())

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{
				GID: "foreign", Status: "active", TotalLength: "1000", CompletedLength: "0", DownloadSpeed: "0",
				Files: filesWithURI("http://example.com/file"),
			},
		},
	}
	monitor := newZeroSpeedMonitor(client, store, "http://127.0.0.1:8080", zap.NewNop(), zeroSpeedMonitorConfig{
		StallThreshold: time.Millisecond,
		PauseDuration:  time.Millisecond,
		ActionTimeout:  time.Second,
	})

	current := time.Unix(100, 0)
	monitor.now = func() time.Time { return current }

	current = current.Add(time.Hour)
	require.NoError(t, monitor.poll(ctx))
	require.Empty(t, client.forcePaused)
}

func TestZeroSpeedMonitorResetsClockAfterAction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:       "stuck",
		TaskID:    "document_stuck",
		CreatedAt: time.Now(),
	}))

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: "stuck", Status: "active", TotalLength: "1000", CompletedLength: "0", DownloadSpeed: "0"},
		},
	}
	monitor := newZeroSpeedMonitor(client, store, "http://127.0.0.1:8080", zap.NewNop(), zeroSpeedMonitorConfig{
		StallThreshold: 2 * time.Second,
		PauseDuration:  time.Millisecond,
		ActionTimeout:  time.Second,
	})

	current := time.Unix(100, 0)
	monitor.now = func() time.Time { return current }

	// Trigger first action.
	require.NoError(t, monitor.poll(ctx))
	current = current.Add(3 * time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Len(t, client.forcePaused, 1)

	// Immediately after action, clock was reset — no second action yet.
	current = current.Add(time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Len(t, client.forcePaused, 1)

	// Another full threshold later — second action fires.
	current = current.Add(3 * time.Second)
	require.NoError(t, monitor.poll(ctx))
	require.Len(t, client.forcePaused, 2)
}
