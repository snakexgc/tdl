package watch

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/application"
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
	w.triggerOnce.Do(func() {
		download := make([]string, 0, len(w.triggerReactions))
		for value := range w.triggerReactions {
			download = append(download, value)
		}
		forward := w.opts.ForwardTriggerReactions
		if w.forward != nil {
			forward = make([]string, 0, len(w.forward.triggerReactions))
			for value := range w.forward.triggerReactions {
				forward = append(forward, value)
			}
		}
		w.trigger, w.triggerStop, w.triggerErr = application.ReactionPolicy(ctx, w.reactionAccount(), download, forward)
	})
	return w.trigger, w.triggerErr
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
