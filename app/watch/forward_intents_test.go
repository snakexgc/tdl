package watch

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/eventbus"
)

type rejectingForwardIntents struct{ received types.ForwardIntent }

const forwardIntentAccount = "alice"

func (p *rejectingForwardIntents) Publish(_ context.Context, intent types.ForwardIntent) error {
	p.received = intent
	return eventbus.ErrFull
}

func TestForwardIntentBackpressurePreservesProtocolReference(t *testing.T) {
	port := &rejectingForwardIntents{}
	w := &Watcher{opts: Options{Account: forwardIntentAccount}, forwardIntents: port}
	err := w.publishForwardIntent(context.Background(), tg.Entities{Channels: map[int64]*tg.Channel{12: {ID: 12, AccessHash: 34}}}, &tg.PeerChannel{ChannelID: 12}, 12, 56)
	require.ErrorIs(t, err, eventbus.ErrFull)
	require.Equal(t, types.ForwardIntent{Account: forwardIntentAccount, Peer: types.MessagePeer{Kind: "channel", ID: 12, AccessHash: 34}, PeerID: 12, MessageID: 56}, port.received)
}

func TestFailedForwardCanBeClaimedAgain(t *testing.T) {
	dedupe := newTimedDedupe(time.Minute)
	require.False(t, dedupe.Seen("message"))
	require.True(t, dedupe.Seen("message"))
	dedupe.Forget("message")
	require.False(t, dedupe.Seen("message"))
}
