package watch

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
)

func (w *Watcher) onReaction(ctx context.Context, e tg.Entities, update *tg.UpdateMessageReactions) error {
	if ctx.Err() != nil {
		return nil
	}
	peerID := tutil.GetPeerID(update.Peer)
	key := w.reactionKey(peerID, update.MsgID)
	policy, err := w.reactionPolicy(ctx)
	if err != nil {
		return err
	}

	peerType := "unknown"
	switch update.Peer.(type) {
	case *tg.PeerUser:
		peerType = peerKindUser
	case *tg.PeerChat:
		peerType = peerKindChat
	case *tg.PeerChannel:
		peerType = peerKindChannel
	}

	logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Reaction update received",
		zap.String("peer_type", peerType),
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", update.MsgID),
		zap.Bool("reactions_min", update.Reactions.Min),
		zap.Int("results_count", len(update.Reactions.Results)),
		zap.Bool("entities_short", e.Short),
		zap.Int("entities_users", len(e.Users)),
		zap.Int("entities_chats", len(e.Chats)),
		zap.Int("entities_channels", len(e.Channels)))

	if w.downloadEnabled() {
		isMine := w.isMyMessageReactions(ctx, &update.Reactions, peerID, update.MsgID)
		if !isMine {
			// A removal or a change to a non-trigger reaction ends this dedupe
			// window, so adding the configured reaction again can enqueue anew.
			if !update.Reactions.Min {
				policy.Forget(key)
			}
			logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Reaction is not mine, skipping",
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", update.MsgID))
		} else {
			inputPeer := w.peerToInputPeer(update.Peer, e)
			if !policy.Claim(ctx, key) {
				logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Duplicate reaction, skipping",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", update.MsgID))
			} else {
				msgLink := w.generateMessageLink(update.Peer, update.MsgID)
				logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Info("My reaction detected, queuing submission",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", update.MsgID),
					zap.Bool("input_peer_nil", inputPeer == nil),
					zap.String("message_link", msgLink))

				if err := ctx.Err(); err != nil {
					logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Watcher is stopping, skipping queued submission",
						zap.Int64("peer_id", peerID),
						zap.Int("msg_id", update.MsgID),
						zap.Error(err))
					policy.Forget(key)
				} else {
					if err := w.enqueueDownload(ctx, downloadJob{peer: inputPeer, msgID: update.MsgID, peerID: peerID, link: msgLink, source: downloadJobSourceReaction}); err != nil {
						logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Warn("Submission queue full, dropping job",
							zap.Int64("peer_id", peerID),
							zap.Int("msg_id", update.MsgID))
						policy.Forget(key)
						w.notify(ctx, "下载队列已满，已丢弃本次触发。\n消息：%s", msgLink)
					}
				}
			}
		}
	}

	// Reaction triggers forward any message the user reacts to, independent of
	// the auto-forward listen set (which only governs new-message forwarding).
	if w.shouldTriggerForwardReaction(ctx, &update.Reactions) {
		return w.publishForwardIntent(ctx, e, update.Peer, peerID, update.MsgID)
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
	if ctx.Err() != nil {
		return nil
	}
	peerID := tutil.GetPeerID(msg.PeerID)
	key := w.reactionKey(peerID, msg.ID)
	policy, err := w.reactionPolicy(ctx)
	if err != nil {
		return err
	}
	if msg.Reactions.GetResults() == nil || len(msg.Reactions.Results) == 0 {
		if !msg.Reactions.Min {
			policy.Forget(key)
		}
		logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("EditMessage has no reactions, skipping",
			zap.Int("msg_id", msg.ID))
		return nil
	}

	logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Reaction detected via EditMessage",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Bool("reactions_min", msg.Reactions.Min),
		zap.Int("results_count", len(msg.Reactions.Results)))

	if w.downloadEnabled() {
		if !w.isMyMessageReactions(ctx, &msg.Reactions, peerID, msg.ID) {
			if !msg.Reactions.Min {
				policy.Forget(key)
			}
			logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Reaction via EditMessage is not mine, skipping",
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", msg.ID))
		} else {
			inputPeer := w.peerToInputPeer(msg.PeerID, e)
			if !policy.Claim(ctx, key) {
				logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Duplicate reaction (via EditMessage), skipping",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", msg.ID))
			} else {
				msgLink := w.generateMessageLink(msg.PeerID, msg.ID)
				logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Info("My reaction detected via EditMessage, queuing submission",
					zap.Int64("peer_id", peerID),
					zap.Int("msg_id", msg.ID),
					zap.Bool("input_peer_nil", inputPeer == nil),
					zap.String("message_link", msgLink))

				if err := ctx.Err(); err != nil {
					logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Debug("Watcher is stopping, skipping queued submission",
						zap.Int64("peer_id", peerID),
						zap.Int("msg_id", msg.ID),
						zap.Error(err))
					policy.Forget(key)
				} else {
					if err := w.enqueueDownload(ctx, downloadJob{peer: inputPeer, msgID: msg.ID, peerID: peerID, link: msgLink, source: downloadJobSourceReaction}); err != nil {
						logctx.From(ctx).With(zap.String("component", "trigger.reaction")).Warn("Submission queue full, dropping job",
							zap.Int64("peer_id", peerID),
							zap.Int("msg_id", msg.ID))
						policy.Forget(key)
						w.notify(ctx, "下载队列已满，已丢弃本次触发。\n消息：%s", msgLink)
					}
				}
			}
		}
	}

	// Reaction triggers forward any message the user reacts to, independent of
	// the auto-forward listen set (which only governs new-message forwarding).
	if w.shouldTriggerForwardReaction(ctx, &msg.Reactions) {
		return w.publishForwardIntent(ctx, e, msg.PeerID, peerID, msg.ID)
	}

	return nil
}

func (w *Watcher) isMyMessageReactions(ctx context.Context, reactions *tg.MessageReactions, _ int64, _ int) bool {
	policy, err := w.reactionPolicy(ctx)
	return err == nil && policy.Matches(ctx, w.reactionInput(reactions, false))
}

func (w *Watcher) shouldTriggerForwardReaction(ctx context.Context, reactions *tg.MessageReactions) bool {
	if !w.forwardEnabled() {
		return false
	}
	policy, err := w.reactionPolicy(ctx)
	return err == nil && policy.Matches(ctx, w.reactionInput(reactions, true))
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
