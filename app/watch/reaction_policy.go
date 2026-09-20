package watch

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func (w *Watcher) reactionAccount() types.AccountID {
	if w.opts.Account != "" {
		return w.opts.Account
	}
	return types.DefaultAccount
}

func (w *Watcher) reactionPolicy(ctx context.Context) (ports.ReactionTrigger, error) {
	if w.opts.Reaction != nil {
		return w.opts.Reaction, nil
	}
	return nil, fmt.Errorf("reaction service is unavailable")
}

func (w *Watcher) reactionKey(peerID int64, messageID int) ports.ReactionKey {
	return ports.ReactionKey{Account: w.reactionAccount(), PeerID: peerID, MessageID: messageID}
}

func (w *Watcher) reactionInput(reactions *tg.MessageReactions, forward bool) ports.ReactionInput {
	in := ports.ReactionInput{Account: w.reactionAccount(), Forward: forward, Partial: reactions == nil}
	if reactions == nil {
		return in
	}
	in.Partial = reactions.Min
	if recent, ok := reactions.GetRecentReactions(); ok {
		for _, r := range recent {
			in.Reactions = append(in.Reactions, ports.Reaction{Value: reactionEmoji(r.Reaction), Mine: r.My})
		}
	}
	for _, r := range reactions.Results {
		_, chosen := r.GetChosenOrder()
		in.Reactions = append(in.Reactions, ports.Reaction{Value: reactionEmoji(r.Reaction), Mine: chosen})
	}
	return in
}
