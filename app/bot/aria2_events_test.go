package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

type rejectedEventTasks struct{ ports.Aria2Tasks }

func (rejectedEventTasks) TellStatus(context.Context, string) (types.Aria2DownloadStatus, error) {
	return types.Aria2DownloadStatus{}, errors.New("task is not registered to this account")
}

type eventNotifications struct {
	ports.Notifications
	count int
}

func (n *eventNotifications) Enqueue(context.Context, types.AccountID, string) error {
	n.count++
	return nil
}

func TestAria2EventsRequireAccountOwnership(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Modules.Aria2 = true
	cfg.Bot.Notify.OnDownloadStart, cfg.Bot.Notify.OnDownloadComplete = true, true
	cfg.Bot.Notify.OnDownloadPause, cfg.Bot.Notify.OnDownloadError = true, true
	ctx := config.WithSource(context.Background(), config.NewSource(cfg))
	service := &eventNotifications{}
	notifier := &botNotifier{account: types.DefaultAccount, service: service}
	tracker := newAria2ProgressTracker()
	defer tracker.Close()
	for _, method := range []string{aria2EventDownloadStart, aria2EventDownloadComplete, aria2EventDownloadPause, aria2EventDownloadError} {
		handleAria2Event(ctx, notifier, func() ports.Aria2Tasks { return rejectedEventTasks{} }, tracker, method, "foreign-gid")
	}
	require.Zero(t, service.count)
}

// TestHandleAria2EventIgnoresEventsWhenConfigNil verifies that when no config is loaded
// (config.Get() == nil), the factory is never called and no messages are sent.
func TestHandleAria2EventIgnoresEventsWhenConfigNil(t *testing.T) {
	notifierBot := &fakeBotAPI{}
	notifier, err := newBotNotifier(context.Background(), types.DefaultAccount, notifierBot, []int64{1})
	require.NoError(t, err)
	t.Cleanup(notifier.Close)
	tracker := newAria2ProgressTracker()

	for _, method := range []string{
		aria2EventDownloadStart,
		aria2EventDownloadPause,
		aria2EventDownloadError,
	} {
		handleAria2Event(context.Background(), notifier, func() ports.Aria2Tasks {
			panic("factory should not be called when config is nil")
		}, tracker, method, "gid-1")
	}
	require.Empty(t, notifierBot.messagesText())
}

func TestRunAria2EventHandlerRecoversPanic(t *testing.T) {
	require.NotPanics(t, func() {
		runAria2EventHandler(context.Background(), aria2EventDownloadStart, "gid-1", func() {
			panic("boom")
		})
	})
}

func TestProgressTrackerCloseCancelsAndDrainsEveryLoop(t *testing.T) {
	tracker := newAria2ProgressTracker()
	cancelled, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	require.True(t, tracker.Run(context.Background(), "gid-1", nil, func(ctx context.Context) {
		<-ctx.Done()
		close(cancelled)
		<-release
	}))
	go func() { tracker.Close(); close(stopped) }()
	<-cancelled
	select {
	case <-stopped:
		t.Fatal("close returned while a loop still owns resources")
	default:
	}
	require.False(t, tracker.Run(context.Background(), "gid-2", nil, func(context.Context) { t.Error("started after close") }))
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("close did not drain")
	}
}

func TestReplacedProgressLoopCannotRemoveNewEntry(t *testing.T) {
	tracker := newAria2ProgressTracker()
	defer tracker.Close()
	oldCancelled, oldReleased := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	require.True(t, tracker.Run(context.Background(), "gid-1", nil, func(ctx context.Context) {
		<-ctx.Done()
		close(oldCancelled)
		<-release
		close(oldReleased)
	}))
	require.True(t, tracker.Run(context.Background(), "gid-1", []trackedMessage{{}}, func(ctx context.Context) { <-ctx.Done() }))
	<-oldCancelled
	close(release)
	<-oldReleased
	require.Len(t, tracker.Cancel("gid-1"), 1)
}

func TestNotifyAria2DownloadCompleteWithoutFiles(t *testing.T) {
	notifierBot := &fakeBotAPI{}
	notifier, err := newBotNotifier(context.Background(), types.DefaultAccount, notifierBot, []int64{1})
	require.NoError(t, err)
	t.Cleanup(notifier.Close)

	notifyAria2DownloadComplete(context.Background(), notifier, aria2.DownloadStatus{GID: "gid-1"})

	require.Eventually(t, func() bool {
		return len(notifierBot.messagesText()) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, []string{"下载完成===> gid-1"}, notifierBot.messagesText())
}

func TestBuildAria2ProgressBar(t *testing.T) {
	tests := []struct {
		completed int64
		total     int64
		width     int
		want      string
	}{
		{0, 0, 10, "[░░░░░░░░░░] 0.00%"},
		{0, 100, 10, "[░░░░░░░░░░] 0.00%"},
		{50, 100, 10, "[█████░░░░░] 50.00%"},
		{100, 100, 10, "[██████████] 100.00%"},
		{25, 100, 10, "[██░░░░░░░░] 25.00%"},
	}
	for _, tt := range tests {
		got := buildAria2ProgressBar(tt.completed, tt.total, tt.width)
		require.Equal(t, tt.want, got)
	}
}
