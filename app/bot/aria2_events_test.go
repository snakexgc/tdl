package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/app/watch"
)

func TestHandleAria2EventIgnoresNonCompleteEvents(t *testing.T) {
	notifierBot := &fakeBotAPI{}
	notifier := newBotNotifier(notifierBot, []int64{1})

	for _, method := range []string{
		"aria2.onDownloadStart",
		"aria2.onDownloadPause",
		"aria2.onDownloadError",
	} {
		called := false
		handleAria2Event(context.Background(), notifier, func() *watch.Aria2Controller {
			called = true
			panic("factory should not be called for non-complete events")
		}, method, "gid-1")

		require.False(t, called)
	}
	require.Empty(t, notifierBot.messagesText())
}

func TestNotifyAria2DownloadCompleteWithoutFiles(t *testing.T) {
	notifierBot := &fakeBotAPI{}
	notifier := newBotNotifier(notifierBot, []int64{1})

	notifyAria2DownloadComplete(context.Background(), notifier, watch.Aria2DownloadStatus{GID: "gid-1"})

	require.Equal(t, []string{"下载完成===> gid-1"}, notifierBot.messagesText())
}
