package watch

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
)

func (w *Watcher) forwardRouter() (ports.ForwardRouting, error) {
	if w.opts.ForwardRouting != nil {
		return w.opts.ForwardRouting, nil
	}
	if w.opts.ForwardQueue != nil {
		return w.opts.ForwardQueue, nil
	}
	return nil, fmt.Errorf("forward routing is unavailable")
}

func (w *Watcher) onNewMessageForward(ctx context.Context, e tg.Entities, update *tg.UpdateNewMessage) error {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}
	return w.forwardUpdateMessage(ctx, e, msg)
}

func (w *Watcher) onNewChannelMessageForward(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}
	return w.forwardUpdateMessage(ctx, e, msg)
}

// The component decides whether a source is selected before we fetch media.
func (w *Watcher) forwardUpdateMessage(ctx context.Context, e tg.Entities, msg *tg.Message) error {
	if !w.forwardEnabled() || msg == nil || msg.Out {
		return nil
	}
	router, err := w.forwardRouter()
	if err != nil {
		return err
	}
	interested, err := router.Interested(ctx, w.reactionAccount(), forwardSourcePeer(msg.PeerID))
	if err != nil || !interested {
		return err
	}
	return w.publishForward(ctx, e, msg.PeerID, tutil.GetPeerID(msg.PeerID), msg.ID, true)
}

func (w *Watcher) processForwardIntent(ctx context.Context, request types.ForwardIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Account != w.reactionAccount() {
		return fmt.Errorf("forward source account mismatch")
	}
	if !w.forwardEnabled() {
		return nil
	}
	router, err := w.forwardRouter()
	if err != nil {
		return err
	}
	input := protocolPeer(request.Peer)
	if input == nil {
		input, err = w.resolvePeer(ctx, request.PeerID)
		if err != nil {
			return err
		}
	}
	msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), input, request.MessageID)
	if err != nil {
		return err
	}
	if msg == nil || !w.forwardEnabled() {
		return nil
	}
	// Persist the access hash before the queue's transport resolves the source.
	peer, err := w.manager.FromInputPeer(ctx, input)
	if err != nil {
		return err
	}
	source := plainPeer(input)
	if actual := forwardSourcePeer(msg.PeerID); actual.Kind != source.Kind || actual.ID != source.ID || msg.ID != request.MessageID {
		return fmt.Errorf("forward message identity does not match its source")
	}
	grouped, _ := msg.GetGroupedID()
	return router.SubmitMessage(ctx, types.ForwardMessage{Account: request.Account, Peer: source, MessageID: msg.ID, GroupedID: grouped, Origin: peer.VisibleName(), Automatic: request.Automatic, Outgoing: msg.Out})
}

func (w *Watcher) publishForwardIntent(ctx context.Context, entities tg.Entities, peer tg.PeerClass, peerID int64, messageID int) error {
	return w.publishForward(ctx, entities, peer, peerID, messageID, false)
}

func (w *Watcher) publishForward(ctx context.Context, entities tg.Entities, peer tg.PeerClass, peerID int64, messageID int, automatic bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.intentMu.RLock()
	port := w.forwardIntents
	w.intentMu.RUnlock()
	if port == nil {
		return fmt.Errorf("forward intent consumer is unavailable")
	}
	return port.Publish(ctx, types.ForwardIntent{Automatic: automatic, Account: w.reactionAccount(), Peer: plainPeer(w.peerToInputPeer(peer, entities)), PeerID: peerID, MessageID: messageID})
}

func forwardSourcePeer(peer tg.PeerClass) types.MessagePeer {
	switch peer := peer.(type) {
	case *tg.PeerUser:
		return types.MessagePeer{Kind: "user", ID: peer.UserID}
	case *tg.PeerChat:
		return types.MessagePeer{Kind: "chat", ID: peer.ChatID}
	case *tg.PeerChannel:
		return types.MessagePeer{Kind: peerKindChannel, ID: peer.ChannelID}
	default:
		return types.MessagePeer{}
	}
}

type watchForwardListening struct{ watcher *Watcher }

func (p watchForwardListening) Listening(ctx context.Context, account types.AccountID) (types.ForwardListening, error) {
	w := p.watcher
	if account != w.reactionAccount() {
		return types.ForwardListening{}, fmt.Errorf("forward listening account mismatch")
	}
	if w.opts.ComponentStore == nil {
		settings := w.opts.ForwardSettings()
		if w.opts.ForwardConfig != nil {
			settings = w.opts.ForwardConfig()
		}
		return types.ForwardListening{Sources: settings.Listen, Comments: settings.ListenComments}, ctx.Err()
	}
	w.intentMu.RLock()
	port, ok := w.forwardIntents.(ports.ForwardListening)
	w.intentMu.RUnlock()
	if !ok {
		return types.ForwardListening{}, fmt.Errorf("forward listening component is unavailable")
	}
	return port.Listening(ctx, account)
}
