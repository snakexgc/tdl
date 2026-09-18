package watch

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/eventbus"
)

type intentRecorder struct {
	request types.DownloadIntent
	err     error
}

func (r *intentRecorder) Publish(_ context.Context, request types.DownloadIntent) error {
	r.request = request
	return r.err
}

func TestDownloadIntentPreservesPeerReferenceAndBackpressure(t *testing.T) {
	for _, peer := range []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 1, AccessHash: 2}, &tg.InputPeerUser{UserID: 3, AccessHash: 4}, &tg.InputPeerChat{ChatID: 5}, &tg.InputPeerSelf{}} {
		require.Equal(t, peer, protocolPeer(plainPeer(peer)))
	}
	port := &intentRecorder{err: eventbus.ErrFull}
	w := &Watcher{opts: Options{Account: "account"}, intents: port, jobCh: make(chan downloadJob, 1)}
	job := downloadJob{peer: &tg.InputPeerChannel{ChannelID: 1, AccessHash: 2}, peerID: 1, msgID: 10, source: downloadJobSourceReaction}
	require.ErrorIs(t, w.enqueueDownload(context.Background(), job), eventbus.ErrFull)
	require.EqualValues(t, "account", port.request.Account)
	require.Equal(t, job.peer, protocolPeer(port.request.Peer))
	require.Empty(t, w.jobCh, "event rejection must not silently submit a second copy")
}
