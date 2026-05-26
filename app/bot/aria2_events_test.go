package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/app/watch"
)

// TestHandleAria2EventIgnoresEventsWhenConfigNil verifies that when no config is loaded
// (config.Get() == nil), the factory is never called and no messages are sent.
func TestHandleAria2EventIgnoresEventsWhenConfigNil(t *testing.T) {
	notifierBot := &fakeBotAPI{}
	notifier := newBotNotifier(notifierBot, []int64{1})
	tracker := newAria2ProgressTracker()

	for _, method := range []string{
		aria2EventDownloadStart,
		aria2EventDownloadPause,
		aria2EventDownloadError,
	} {
		handleAria2Event(context.Background(), notifier, func() *watch.Aria2Controller {
			panic("factory should not be called when config is nil")
		}, tracker, method, "gid-1")
	}
	require.Empty(t, notifierBot.messagesText())
}

func TestNotifyAria2DownloadCompleteWithoutFiles(t *testing.T) {
	notifierBot := &fakeBotAPI{}
	notifier := newBotNotifier(notifierBot, []int64{1})

	notifyAria2DownloadComplete(context.Background(), notifier, watch.Aria2DownloadStatus{GID: "gid-1"})

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
