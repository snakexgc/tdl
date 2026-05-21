package watch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	appforward "github.com/iyear/tdl/app/forward"
	"github.com/iyear/tdl/core/forwarder"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/tutil"
)

type forwardRuntime struct {
	enabled bool
	mode    forwarder.Mode
	target  peers.Peer
	listen  map[int64]forwardListenEntry
	dedupe  *timedDedupe
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

	if len(listen) == 0 {
		w.notify(ctx, "监听转发已启用，但 forward.listen 里没有可用对象。")
		logctx.From(ctx).Warn("Forward listening enabled without valid peers")
		return
	}

	w.forward = &forwardRuntime{
		enabled: true,
		mode:    mode,
		target:  target,
		listen:  listen,
		dedupe:  newTimedDedupe(w.opts.ForwardDedupeTTL),
	}

	names := make([]string, 0, len(listen))
	for _, entry := range listen {
		if entry.IsComment {
			names = append(names, fmt.Sprintf("%s(%d, comments of %d)", entry.DisplayName, entry.PeerID, entry.LinkedFrom))
		} else {
			names = append(names, fmt.Sprintf("%s(%d)", entry.DisplayName, entry.PeerID))
		}
	}
	logctx.From(ctx).Info("Forward listening configured",
		zap.String("mode", appforward.ConfigModeName(mode)),
		zap.Int64("target_id", target.ID()),
		zap.Strings("listen", names))
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

func (w *Watcher) forwardUpdateMessage(ctx context.Context, e tg.Entities, msg *tg.Message) error {
	if w.forward == nil || !w.forward.enabled || msg == nil || msg.Out {
		return nil
	}

	peerID := tutil.GetPeerID(msg.PeerID)
	entry, ok := w.forward.listen[peerID]
	if !ok {
		return nil
	}

	key := forwardDedupeKey(peerID, msg)
	if w.forward.dedupe.Seen(key) {
		logctx.From(ctx).Debug("Duplicate forward update skipped",
			zap.String("key", key),
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return nil
	}

	from, err := w.resolveForwardPeer(ctx, e, msg.PeerID, peerID)
	if err != nil {
		w.notify(ctx, "监听转发失败：无法解析来源。\n来源：%d\n消息：%d\n错误：%v", peerID, msg.ID, err)
		return nil
	}

	logctx.From(ctx).Info("Forwarding watched message",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Int64("target_id", w.forward.target.ID()),
		zap.Bool("is_comment", entry.IsComment),
		zap.Int64("linked_from", entry.LinkedFrom))

	go func() {
		err := appforward.ForwardSingle(ctx, w.pool, from, msg, w.forward.target, appforward.ElemOptions{
			Mode:    w.forward.mode,
			Silent:  w.opts.ForwardSilent,
			Grouped: true,
		}, w.opts.Threads)
		if err != nil && !errors.Is(err, context.Canceled) {
			notifyCtx := context.WithoutCancel(ctx)
			logctx.From(notifyCtx).Error("Watched message forward failed",
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", msg.ID),
				zap.Error(err))
			w.notify(notifyCtx, "监听转发失败。\n来源：%d\n消息：%d\n错误：%v", peerID, msg.ID, err)
			return
		}
		if err == nil {
			w.notify(context.WithoutCancel(ctx), "监听转发完成。\n来源：%d\n消息：%d\n目标：%s", peerID, msg.ID, w.forward.target.VisibleName())
		}
	}()

	return nil
}

func forwardDedupeKey(peerID int64, msg *tg.Message) string {
	if groupedID, ok := msg.GetGroupedID(); ok && groupedID != 0 {
		return fmt.Sprintf("forward:%d:g:%d", peerID, groupedID)
	}
	return fmt.Sprintf("forward:%d:m:%d", peerID, msg.ID)
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
