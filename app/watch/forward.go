package watch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	appforward "github.com/iyear/tdl/app/forward"
	"github.com/iyear/tdl/core/forwarder"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/tutil"
)

type forwardRuntime struct {
	enabled          bool
	mode             forwarder.Mode
	target           peers.Peer
	listen           map[int64]forwardListenEntry
	dedupe           *timedDedupe
	triggerReactions map[string]struct{}
}

type forwardListenEntry struct {
	Input       string
	PeerID      int64
	LinkedFrom  int64
	IsComment   bool
	DisplayName string
}

type timedDedupe struct {
	mu     sync.Mutex
	values map[string]time.Time
	ttl    time.Duration
}

func newTimedDedupe(ttl time.Duration) *timedDedupe {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &timedDedupe{
		values: make(map[string]time.Time),
		ttl:    ttl,
	}
}

func (d *timedDedupe) Seen(key string) bool {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pruneLocked(now)
	if expire, ok := d.values[key]; ok && expire.After(now) {
		return true
	}
	d.values[key] = now.Add(d.ttl)
	return false
}

func (d *timedDedupe) pruneLocked(now time.Time) {
	for key, expire := range d.values {
		if !expire.After(now) {
			delete(d.values, key)
		}
	}
}

func (w *Watcher) configureForward(ctx context.Context) {
	w.forward = nil
	if !w.opts.Forward {
		return
	}

	mode, err := appforward.NormalizeMode(w.opts.ForwardMode)
	if err != nil {
		w.notify(ctx, "监听转发未启用：forward.mode 配置错误：%v", err)
		logctx.From(ctx).Error("Invalid forward mode", zap.Error(err))
		return
	}
	target, err := appforward.ResolvePeer(ctx, w.manager, w.opts.ForwardTarget)
	if err != nil {
		w.notify(ctx, "监听转发未启用：无法解析转发目标 %q：%v", w.opts.ForwardTarget, err)
		logctx.From(ctx).Error("Cannot resolve forward target",
			zap.String("target", w.opts.ForwardTarget),
			zap.Error(err))
		return
	}

	listen := make(map[int64]forwardListenEntry)
	for _, raw := range w.opts.ForwardListen {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		peer, err := appforward.ResolvePeer(ctx, w.manager, raw)
		if err != nil {
			w.notify(ctx, "监听转发跳过 %q：无法解析：%v", raw, err)
			logctx.From(ctx).Warn("Cannot resolve forward listen peer",
				zap.String("peer", raw),
				zap.Error(err))
			continue
		}
		listen[peer.ID()] = forwardListenEntry{
			Input:       raw,
			PeerID:      peer.ID(),
			DisplayName: peer.VisibleName(),
		}
		w.addLinkedDiscussion(ctx, listen, raw, peer)
	}

	triggerReactions := newTriggerReactionSet(w.opts.ForwardTriggerReactions)

	// Forward runs via auto-listen peers and/or reaction triggers. An empty
	// trigger set means "react with any emoji to forward" (mirroring the
	// download trigger), so once the module is enabled there is always a way to
	// forward — no need to bail out on empty listen/triggers.
	w.forward = &forwardRuntime{
		enabled:          true,
		mode:             mode,
		target:           target,
		listen:           listen,
		dedupe:           newTimedDedupe(w.opts.ForwardDedupeTTL),
		triggerReactions: triggerReactions,
	}

	names := make([]string, 0, len(listen))
	for _, entry := range listen {
		if entry.IsComment {
			names = append(names, fmt.Sprintf("%s(%d, comments of %d)", entry.DisplayName, entry.PeerID, entry.LinkedFrom))
		} else {
			names = append(names, fmt.Sprintf("%s(%d)", entry.DisplayName, entry.PeerID))
		}
	}
	logctx.From(ctx).Info("Forward configured",
		zap.String("mode", appforward.ConfigModeName(mode)),
		zap.Int64("target_id", target.ID()),
		zap.Strings("listen", names),
		zap.Int("trigger_reactions", len(triggerReactions)))
}

func (w *Watcher) addLinkedDiscussion(ctx context.Context, listen map[int64]forwardListenEntry, input string, peer peers.Peer) {
	if !w.opts.ForwardListenComments {
		return
	}
	ch, ok := peer.(peers.Channel)
	if !ok || !ch.IsBroadcast() {
		return
	}
	full, err := ch.FullRaw(ctx)
	if err != nil {
		logctx.From(ctx).Warn("Cannot load channel full info for comments",
			zap.Int64("channel_id", peer.ID()),
			zap.Error(err))
		return
	}
	linkedID, ok := full.GetLinkedChatID()
	if !ok || linkedID == 0 {
		return
	}
	listen[linkedID] = forwardListenEntry{
		Input:       input,
		PeerID:      linkedID,
		LinkedFrom:  peer.ID(),
		IsComment:   true,
		DisplayName: "comments",
	}
	logctx.From(ctx).Info("Forward listening added linked discussion",
		zap.Int64("channel_id", peer.ID()),
		zap.Int64("linked_chat_id", linkedID))
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

// forwardUpdateMessage auto-forwards a new message, but only from explicitly
// listened peers. Reaction triggers use enqueueForwardMessage directly and are
// not restricted to the listen set.
func (w *Watcher) forwardUpdateMessage(ctx context.Context, e tg.Entities, msg *tg.Message) error {
	if w.forward == nil || !w.forward.enabled || msg == nil || msg.Out {
		return nil
	}
	peerID := tutil.GetPeerID(msg.PeerID)
	if _, ok := w.forward.listen[peerID]; !ok {
		return nil
	}
	w.enqueueForwardMessage(ctx, e, msg)
	return nil
}

// enqueueForwardMessage dedupes and enqueues a single message for forwarding to
// the configured target. It deliberately does NOT apply the listen-peer filter,
// so it serves both auto-forwarding (after a listen check) and reaction triggers
// (which forward any message the user reacts to, on any peer).
func (w *Watcher) enqueueForwardMessage(ctx context.Context, e tg.Entities, msg *tg.Message) {
	if w.forward == nil || !w.forward.enabled || msg == nil || msg.Out {
		return
	}
	peerID := tutil.GetPeerID(msg.PeerID)

	key := forwardDedupeKey(peerID, msg)
	if w.forward.dedupe.Seen(key) {
		logctx.From(ctx).Debug("Duplicate forward update skipped",
			zap.String("key", key),
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return
	}

	// Resolve the source peer so the manager caches its access hash; the queue
	// worker re-resolves it by id when it later runs the job.
	from, err := w.resolveForwardPeer(ctx, e, msg.PeerID, peerID)
	if err != nil {
		w.notify(ctx, "监听转发失败：无法解析来源。\n来源：%d\n消息：%d\n错误：%v", peerID, msg.ID, err)
		return
	}
	originName := from.VisibleName()

	logctx.From(ctx).Info("Enqueuing message for forward",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Int64("target_id", w.forward.target.ID()))

	if _, err := appforward.Jobs().EnqueueMessage(ctx, peerID, msg.ID, originName,
		w.opts.ForwardTarget, w.forward.target.VisibleName(),
		appforward.ConfigModeName(w.forward.mode), w.opts.ForwardSilent); err != nil {
		notifyCtx := context.WithoutCancel(ctx)
		logctx.From(notifyCtx).Error("Enqueue forward failed",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID),
			zap.Error(err))
		w.notify(notifyCtx, "监听转发入队失败。\n来源：%d\n消息：%d\n错误：%v", peerID, msg.ID, err)
	}
}

func forwardDedupeKey(peerID int64, msg *tg.Message) string {
	if groupedID, ok := msg.GetGroupedID(); ok && groupedID != 0 {
		return fmt.Sprintf("forward:%d:g:%d", peerID, groupedID)
	}
	return fmt.Sprintf("forward:%d:m:%d", peerID, msg.ID)
}

func (w *Watcher) triggerForwardOnReaction(ctx context.Context, e tg.Entities, peer tg.PeerClass, peerID int64, msgID int) {
	inputPeer := w.peerToInputPeer(peer, e)
	if inputPeer == nil {
		var err error
		inputPeer, err = w.resolvePeer(ctx, peerID)
		if err != nil {
			w.notify(ctx, "监听转发（回应触发）失败：无法解析来源。\n来源：%d\n消息：%d\n错误：%v", peerID, msgID, err)
			return
		}
	}
	msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), inputPeer, msgID)
	if err != nil {
		w.notify(ctx, "监听转发（回应触发）失败：无法获取消息。\n来源：%d\n消息：%d\n错误：%v", peerID, msgID, err)
		return
	}
	// Reaction triggers forward the reacted message regardless of the listen set.
	w.enqueueForwardMessage(ctx, e, msg)
}

func (w *Watcher) resolveForwardPeer(ctx context.Context, e tg.Entities, peer tg.PeerClass, peerID int64) (peers.Peer, error) {
	input := w.peerToInputPeer(peer, e)
	if input == nil {
		var err error
		input, err = w.resolvePeer(ctx, peerID)
		if err != nil {
			return nil, err
		}
	}
	return w.manager.FromInputPeer(ctx, input)
}
