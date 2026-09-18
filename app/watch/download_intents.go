package watch

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/eventbus"
)

const (
	peerKindChannel = "channel"
	peerKindUser    = "user"
	peerKindChat    = "chat"
)

func (w *Watcher) enqueueDownload(ctx context.Context, job downloadJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.intentMu.RLock()
	port := w.intents
	w.intentMu.RUnlock()
	if port != nil {
		return port.Publish(ctx, types.DownloadIntent{Account: w.reactionAccount(), Peer: plainPeer(job.peer), PeerID: job.peerID, MessageID: job.msgID, Link: job.link, Source: job.source})
	}
	// Pre-connection and standalone adapters retain their bounded local queue.
	select {
	case w.jobCh <- job:
		return nil
	default:
		return eventbus.ErrFull
	}
}

func plainPeer(peer tg.InputPeerClass) types.MessagePeer {
	switch value := peer.(type) {
	case *tg.InputPeerChannel:
		return types.MessagePeer{Kind: peerKindChannel, ID: value.ChannelID, AccessHash: value.AccessHash}
	case *tg.InputPeerUser:
		return types.MessagePeer{Kind: peerKindUser, ID: value.UserID, AccessHash: value.AccessHash}
	case *tg.InputPeerChat:
		return types.MessagePeer{Kind: peerKindChat, ID: value.ChatID}
	case *tg.InputPeerSelf:
		return types.MessagePeer{Kind: "self"}
	default:
		return types.MessagePeer{}
	}
}

func protocolPeer(peer types.MessagePeer) tg.InputPeerClass {
	switch peer.Kind {
	case peerKindChannel:
		return &tg.InputPeerChannel{ChannelID: peer.ID, AccessHash: peer.AccessHash}
	case peerKindUser:
		return &tg.InputPeerUser{UserID: peer.ID, AccessHash: peer.AccessHash}
	case peerKindChat:
		return &tg.InputPeerChat{ChatID: peer.ID}
	case "self":
		return &tg.InputPeerSelf{}
	default:
		return nil
	}
}
