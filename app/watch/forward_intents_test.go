package watch

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/eventbus"
)

type fixedForwardRules struct{}

func (fixedForwardRules) Destinations(context.Context, types.ChatRef) []types.ForwardDestination {
	return []types.ForwardDestination{{Target: "channel:99", Mode: testForwardMode}}
}

func TestRuleForwardingWorksWithoutLegacyDefaultTarget(t *testing.T) {
	port := &rejectingForwardIntents{}
	w := &Watcher{opts: Options{Account: forwardIntentAccount, Forward: true, ForwardRouting: interestedForwardRouter{}}, forwardIntents: port}
	err := w.forwardUpdateMessage(context.Background(), tg.Entities{Channels: map[int64]*tg.Channel{12: {ID: 12, AccessHash: 34}}}, &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 12}, ID: 56})
	require.ErrorIs(t, err, eventbus.ErrFull)
	require.True(t, port.received.Automatic)
	require.Equal(t, int64(12), port.received.Peer.ID)
}

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
	require.Equal(t, types.ForwardIntent{Account: forwardIntentAccount, Peer: types.MessagePeer{Kind: peerKindChannel, ID: 12, AccessHash: 34}, PeerID: 12, MessageID: 56}, port.received)
}

type interestedForwardRouter struct{}

func (interestedForwardRouter) Interested(context.Context, types.AccountID, types.MessagePeer) (bool, error) {
	return true, nil
}
func (interestedForwardRouter) SubmitMessage(context.Context, types.ForwardMessage) error { return nil }
