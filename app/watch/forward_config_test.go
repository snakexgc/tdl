package watch

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	appforward "github.com/snakexgc/tdl/app/forward"
	"github.com/snakexgc/tdl/application/forwarder"
	"github.com/snakexgc/tdl/interfaces/types"
)

const testForwardMode = "default"

func TestForwardPeerAdapterPreservesTypesWithoutNetwork(t *testing.T) {
	ctx := context.Background()
	manager := peers.Options{Cache: &peers.InmemoryCache{}}.Build(tg.NewClient(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		return fmt.Errorf("unexpected network request")
	})))
	require.NoError(t, manager.Apply(ctx, []tg.UserClass{&tg.User{ID: 1, AccessHash: 111, FirstName: "user"}}, []tg.ChatClass{&tg.Chat{ID: 1, Title: "chat"}, &tg.Channel{ID: 1, AccessHash: 222, Title: peerKindChannel}}))
	runtime := appforward.Runtime{Account: types.DefaultAccount, Manager: manager}
	for _, ref := range []string{"user:1", "chat:1", "channel:1"} {
		got, err := runtime.ResolveForwardPeer(ctx, types.DefaultAccount, ref, false)
		require.NoError(t, err)
		require.Equal(t, types.ChatRef(ref), got.Reference)
		require.NotEmpty(t, got.Name)
	}
	_, err := runtime.ResolveForwardPeer(ctx, "another", "chat:1", false)
	require.Error(t, err)
}

type acceptedForwardIntents struct{ received types.ForwardIntent }

func (p *acceptedForwardIntents) Publish(_ context.Context, request types.ForwardIntent) error {
	p.received = request
	return nil
}

func TestWatchForwardProtocolFeedsComponentRouter(t *testing.T) {
	ctx := context.Background()
	queue := appforward.NewQueue(newMemoryTaskStorage())
	router := forwarder.NewMessageRouter(ctx, forwardIntentAccount, queue.ForwardQueue, forwarder.RoutingOptions{Rules: fixedForwardRules{}})
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	wrongPeer, calls := false, 0
	client := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		request, ok := in.(*tg.MessagesGetHistoryRequest)
		if !ok {
			return fmt.Errorf("unexpected request %T", in)
		}
		calls++
		require.Equal(t, int64(12), request.Peer.(*tg.InputPeerChannel).ChannelID)
		peerID := int64(12)
		if wrongPeer {
			peerID++
		}
		message := &tg.Message{ID: request.OffsetID - 1, PeerID: &tg.PeerChannel{ChannelID: peerID}, Message: "fixture"}
		message.SetGroupedID(77)
		response := &tg.MessagesMessages{Messages: []tg.MessageClass{message}}
		buffer := &bin.Buffer{}
		if err := response.Encode(buffer); err != nil {
			return err
		}
		return out.Decode(buffer)
	}))
	manager := peers.Options{Cache: &peers.InmemoryCache{}}.Build(client)
	require.NoError(t, manager.Apply(ctx, nil, []tg.ChatClass{&tg.Channel{ID: 12, AccessHash: 34, Title: "source"}}))
	intents := &acceptedForwardIntents{}
	w := &Watcher{opts: Options{Account: forwardIntentAccount, Forward: true, ForwardRouting: router}, manager: manager, pool: liveSinglePool{api: client}, forwardIntents: intents}
	event := &tg.Message{ID: 56, PeerID: &tg.PeerChannel{ChannelID: 12}}
	require.NoError(t, w.forwardUpdateMessage(ctx, tg.Entities{Channels: map[int64]*tg.Channel{12: {ID: 12, AccessHash: 34}}}, event))
	require.True(t, intents.received.Automatic)
	require.NoError(t, w.processForwardIntent(ctx, intents.received))
	intents.received.MessageID++
	require.NoError(t, w.processForwardIntent(ctx, intents.received))
	jobs, err := queue.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1, "album replay deduplication belongs to the component")
	require.Equal(t, peerKindChannel, jobs[0].SourcePeerKind)
	require.Equal(t, int64(12), jobs[0].SourcePeerID)
	require.Equal(t, "channel:99", jobs[0].Destination)
	wrongPeer = true
	require.ErrorContains(t, w.processForwardIntent(ctx, intents.received), "identity")
	intents.received.Account = "another"
	require.ErrorContains(t, w.processForwardIntent(ctx, intents.received), "account mismatch")
	require.Equal(t, 3, calls, "wrong accounts are rejected before Telegram access")
}
