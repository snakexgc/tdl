package forward

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/types"
)

func (rt Runtime) ResolveForwardPeer(ctx context.Context, account types.AccountID, raw string, comments bool) (types.ForwardPeer, error) {
	owner := rt.Account
	if owner == "" {
		owner = types.DefaultAccount
	}
	if account != owner || rt.Manager == nil {
		return types.ForwardPeer{}, fmt.Errorf("forward peer resolver is unavailable for this account")
	}
	peer, err := ResolvePeer(ctx, rt.Manager, raw)
	if err != nil {
		return types.ForwardPeer{}, err
	}
	result := types.ForwardPeer{Name: peer.VisibleName()}
	switch input := peer.InputPeer().(type) {
	case *tg.InputPeerUser:
		result.Reference = types.ChatRef(fmt.Sprintf("user:%d", input.UserID))
	case *tg.InputPeerChat:
		result.Reference = types.ChatRef(fmt.Sprintf("chat:%d", input.ChatID))
	case *tg.InputPeerChannel:
		result.Reference = types.ChatRef(fmt.Sprintf("channel:%d", input.ChannelID))
	case *tg.InputPeerSelf:
		result.Reference = "self"
	default:
		return types.ForwardPeer{}, fmt.Errorf("unsupported resolved forward peer %T", input)
	}
	if channel, ok := peer.(peers.Channel); comments && ok && channel.IsBroadcast() {
		full, err := channel.FullRaw(ctx)
		if err != nil {
			return result, err // The parent source remains usable without discussion metadata.
		}
		if linked, ok := full.GetLinkedChatID(); ok && linked != 0 {
			result.LinkedDiscussion = types.ChatRef(fmt.Sprintf("channel:%d", linked))
		}
	}
	return result, nil
}
