package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/tutil"
)

func (w *Watcher) onReaction(ctx context.Context, e tg.Entities, update *tg.UpdateMessageReactions) error {
	peerID := tutil.GetPeerID(update.Peer)

	peerType := "unknown"
	switch update.Peer.(type) {
	case *tg.PeerUser:
		peerType = "user"
	case *tg.PeerChat:
		peerType = "chat"
	case *tg.PeerChannel:
		peerType = "channel"
	}

	reactionsJSON, _ := json.Marshal(update.Reactions)
	logctx.From(ctx).Info("Reaction update received",
		zap.String("peer_type", peerType),
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", update.MsgID),
		zap.Bool("reactions_min", update.Reactions.Min),
		zap.Int("results_count", len(update.Reactions.Results)),
		zap.String("reactions_json", string(reactionsJSON)),
		zap.Bool("entities_short", e.Short),
		zap.Int("entities_users", len(e.Users)),
		zap.Int("entities_chats", len(e.Chats)),
		zap.Int("entities_channels", len(e.Channels)))

	if w.opts.Download {
		isMine := w.isMyMessageReactions(ctx, &update.Reactions, peerID, update.MsgID)
		if !isMine {
			logctx.From(ctx).Info("Reaction is not mine, skipping",
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", update.MsgID))
		} else {
			inputPeer := w.peerToInputPeer(update.Peer, e)
			key := fmt.Sprintf("%d:%d", peerID, update.MsgID)
			if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
				logctx.From(ctx).Info("Duplicate reaction, skipping",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", update.MsgID))
			} else {
				msgLink := w.generateMessageLink(update.Peer, update.MsgID)
				logctx.From(ctx).Info("My reaction detected, queuing submission",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", update.MsgID),
					zap.Bool("input_peer_nil", inputPeer == nil),
					zap.String("message_link", msgLink))

				color.Cyan("📌 My reaction on %d/%d, queueing submission...", peerID, update.MsgID)
				color.Green("🔗 Message link: %s", msgLink)

				if err := ctx.Err(); err != nil {
					logctx.From(ctx).Info("Watcher is stopping, skipping queued submission",
						zap.Int64("peer_id", peerID),
						zap.Int("msg_id", update.MsgID),
						zap.Error(err))
					w.dedup.Delete(key)
				} else {
					select {
					case w.jobCh <- downloadJob{peer: inputPeer, msgID: update.MsgID, peerID: peerID, link: msgLink, source: downloadJobSourceReaction}:
					default:
						logctx.From(ctx).Warn("Submission queue full, dropping job",
							zap.Int64("peer_id", peerID),
							zap.Int("msg_id", update.MsgID))
						w.dedup.Delete(key)
						w.notify(ctx, "下载队列已满，已丢弃本次触发。\n消息：%s", msgLink)
					}
				}
			}
		}
	}

	// Reaction triggers forward any message the user reacts to, independent of
	// the auto-forward listen set (which only governs new-message forwarding).
	if w.shouldTriggerForwardReaction(&update.Reactions) {
		go w.triggerForwardOnReaction(ctx, e, update.Peer, peerID, update.MsgID)
	}

	return nil
}

func (w *Watcher) onEditMessage(ctx context.Context, e tg.Entities, update *tg.UpdateEditMessage) error {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}
	return w.onEditMessageReaction(ctx, e, msg)
}

func (w *Watcher) onEditChannelMessage(ctx context.Context, e tg.Entities, update *tg.UpdateEditChannelMessage) error {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}
	return w.onEditMessageReaction(ctx, e, msg)
}

func (w *Watcher) onEditMessageReaction(ctx context.Context, e tg.Entities, msg *tg.Message) error {
	if msg.Reactions.GetResults() == nil || len(msg.Reactions.Results) == 0 {
		logctx.From(ctx).Debug("EditMessage has no reactions, skipping",
			zap.Int("msg_id", msg.ID))
		return nil
	}

	peerID := tutil.GetPeerID(msg.PeerID)
	reactionsJSON, _ := json.Marshal(msg.Reactions)
	logctx.From(ctx).Info("Reaction detected via EditMessage",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Bool("reactions_min", msg.Reactions.Min),
		zap.Int("results_count", len(msg.Reactions.Results)),
		zap.String("reactions_json", string(reactionsJSON)))

	if w.opts.Download {
		if !w.isMyMessageReactions(ctx, &msg.Reactions, peerID, msg.ID) {
			logctx.From(ctx).Info("Reaction via EditMessage is not mine, skipping",
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", msg.ID))
		} else {
			inputPeer := w.peerToInputPeer(msg.PeerID, e)
			key := fmt.Sprintf("%d:%d", peerID, msg.ID)
			if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
				logctx.From(ctx).Info("Duplicate reaction (via EditMessage), skipping",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", msg.ID))
			} else {
				msgLink := w.generateMessageLink(msg.PeerID, msg.ID)
				logctx.From(ctx).Info("My reaction detected via EditMessage, queuing submission",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", msg.ID),
					zap.Bool("input_peer_nil", inputPeer == nil),
					zap.String("message_link", msgLink))

				color.Cyan("📌 My reaction on %d/%d (via edit), queueing submission...", peerID, msg.ID)
				color.Green("🔗 Message link: %s", msgLink)

				if err := ctx.Err(); err != nil {
					logctx.From(ctx).Info("Watcher is stopping, skipping queued submission",
						zap.Int64("peer_id", peerID),
						zap.Int("msg_id", msg.ID),
						zap.Error(err))
					w.dedup.Delete(key)
				} else {
					select {
					case w.jobCh <- downloadJob{peer: inputPeer, msgID: msg.ID, peerID: peerID, link: msgLink, source: downloadJobSourceReaction}:
					default:
						logctx.From(ctx).Warn("Submission queue full, dropping job",
							zap.Int64("peer_id", peerID),
							zap.Int("msg_id", msg.ID))
						w.dedup.Delete(key)
						w.notify(ctx, "下载队列已满，已丢弃本次触发。\n消息：%s", msgLink)
					}
				}
			}
		}
	}

	// Reaction triggers forward any message the user reacts to, independent of
	// the auto-forward listen set (which only governs new-message forwarding).
	if w.shouldTriggerForwardReaction(&msg.Reactions) {
		go w.triggerForwardOnReaction(ctx, e, msg.PeerID, peerID, msg.ID)
	}

	return nil
}

func (w *Watcher) isMyMessageReactions(ctx context.Context, reactions *tg.MessageReactions, peerID int64, msgID int) bool {
	if reactions == nil {
		return false
	}
	if reactions.Min {
		logctx.From(ctx).Info("Reactions.Min=true, cannot determine ownership, skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msgID))
		return false
	}

	myReactionSeen := false
	if recent, ok := reactions.GetRecentReactions(); ok {
		for _, r := range recent {
			emoji := reactionEmoji(r.Reaction)
			logctx.From(ctx).Debug("Checking RecentReaction",
				zap.Bool("my", r.My),
				zap.String("emoji", emoji),
				zap.Int64("peer_id", tutil.GetPeerID(r.PeerID)))
			if r.My {
				myReactionSeen = true
				if !w.matchesTriggerReaction(emoji) {
					logctx.From(ctx).Info("My reaction does not match configured trigger, skipping",
						zap.String("emoji", emoji),
						zap.Int64("peer_id", peerID),
						zap.Int("msg_id", msgID))
					continue
				}
				logctx.From(ctx).Info("Found my reaction via RecentReactions",
					zap.String("emoji", emoji),
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", msgID))
				return true
			}
		}
	}

	for _, rc := range reactions.Results {
		emoji := reactionEmoji(rc.Reaction)
		chosenOrder, ok := rc.GetChosenOrder()
		logctx.From(ctx).Debug("Checking Result ChosenOrder",
			zap.String("emoji", emoji),
			zap.Int("count", rc.Count),
			zap.Bool("chosen", ok),
			zap.Int("chosen_order", chosenOrder))
		if ok {
			myReactionSeen = true
			if !w.matchesTriggerReaction(emoji) {
				logctx.From(ctx).Info("My reaction via ChosenOrder does not match configured trigger, skipping",
					zap.String("emoji", emoji),
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", msgID))
				continue
			}
			logctx.From(ctx).Info("Found my reaction via ChosenOrder",
				zap.String("emoji", emoji),
				zap.Int("chosen_order", chosenOrder),
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", msgID))
			return true
		}
	}

	if myReactionSeen {
		logctx.From(ctx).Info("My reaction did not match any configured trigger",
			zap.Int("results_count", len(reactions.Results)),
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msgID))
		return false
	}

	logctx.From(ctx).Info("Reaction is NOT mine (both methods failed)",
		zap.Int("results_count", len(reactions.Results)),
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msgID))
	return false
}

func (w *Watcher) matchesTriggerReaction(emoji string) bool {
	if len(w.triggerReactions) == 0 {
		return true
	}
	_, ok := w.triggerReactions[normalizeTriggerReaction(emoji)]
	return ok
}

// shouldTriggerForwardReaction reports whether a reaction update should trigger
// an on-demand forward: forwarding must be enabled and the current user's
// reaction must match the forward trigger set. An empty trigger set matches any
// emoji (mirroring the download trigger). It is deliberately independent of the
// auto-forward listen set, so reacting forwards a message from any peer.
func (w *Watcher) shouldTriggerForwardReaction(reactions *tg.MessageReactions) bool {
	return w.forward != nil && w.forward.enabled && w.hasMyForwardReactionTrigger(reactions)
}

// matchesForwardTrigger reports whether an emoji matches the forward trigger
// set. An empty set matches every emoji.
func (w *Watcher) matchesForwardTrigger(emoji string) bool {
	if w.forward == nil || len(w.forward.triggerReactions) == 0 {
		return true
	}
	_, ok := w.forward.triggerReactions[normalizeTriggerReaction(emoji)]
	return ok
}

func (w *Watcher) hasMyForwardReactionTrigger(reactions *tg.MessageReactions) bool {
	if w.forward == nil || reactions == nil || reactions.Min {
		return false
	}
	if recent, ok := reactions.GetRecentReactions(); ok {
		for _, r := range recent {
			if r.My && w.matchesForwardTrigger(reactionEmoji(r.Reaction)) {
				return true
			}
		}
	}
	for _, rc := range reactions.Results {
		if _, ok := rc.GetChosenOrder(); ok && w.matchesForwardTrigger(reactionEmoji(rc.Reaction)) {
			return true
		}
	}
	return false
}

func newTriggerReactionSet(values []string) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeTriggerReaction(value)
		if value == "" {
			continue
		}
		m[value] = struct{}{}
	}
	return m
}

func normalizeTriggerReaction(value string) string {
	return strings.TrimSpace(value)
}

func reactionEmoji(r tg.ReactionClass) string {
	switch v := r.(type) {
	case *tg.ReactionEmoji:
		return v.Emoticon
	case *tg.ReactionCustomEmoji:
		return fmt.Sprintf("custom:%d", v.DocumentID)
	case *tg.ReactionPaid:
		return "paid"
	default:
		return fmt.Sprintf("unknown:%T", r)
	}
}

func (w *Watcher) generateMessageLink(peer tg.PeerClass, msgID int) string {
	switch p := peer.(type) {
	case *tg.PeerChannel:
		return fmt.Sprintf("https://t.me/c/%d/%d", p.ChannelID, msgID)
	case *tg.PeerChat:
		return fmt.Sprintf("https://t.me/c/%d/%d", p.ChatID, msgID)
	case *tg.PeerUser:
		return fmt.Sprintf("tg://openmessage?user_id=%d&message_id=%d", p.UserID, msgID)
	default:
		return fmt.Sprintf("(unknown peer type: %T, msg_id=%d)", peer, msgID)
	}
}

func (w *Watcher) peerToInputPeer(peer tg.PeerClass, e tg.Entities) tg.InputPeerClass {
	switch p := peer.(type) {
	case *tg.PeerUser:
		if u, ok := e.Users[p.UserID]; ok {
			return &tg.InputPeerUser{
				UserID:     u.ID,
				AccessHash: u.AccessHash,
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{
			ChatID: p.ChatID,
		}
	case *tg.PeerChannel:
		if ch, ok := e.Channels[p.ChannelID]; ok {
			return &tg.InputPeerChannel{
				ChannelID:  ch.ID,
				AccessHash: ch.AccessHash,
			}
		}
	}
	return nil
}
