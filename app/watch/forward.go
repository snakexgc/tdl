package watch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	appforward "github.com/snakexgc/tdl/app/forward"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/forwarder"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
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

func (d *timedDedupe) Forget(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.values, key)
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
		w.notify(ctx, "旧版默认目标无法解析 %q：%v；分组规则仍可独立转发。", w.opts.ForwardTarget, err)
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
	if !w.opts.Forward || msg == nil || msg.Out {
		return nil
	}
	peerID := tutil.GetPeerID(msg.PeerID)
	legacy := false
	if w.forward != nil && w.forward.enabled {
		_, legacy = w.forward.listen[peerID]
	}
	if !legacy && len(w.ruleDestinations(ctx, msg.PeerID)) == 0 {
		return nil
	}
	return w.publishAutomaticForwardIntent(ctx, e, msg.PeerID, peerID, msg.ID)
}

// enqueueForwardMessage dedupes and enqueues a single message for forwarding to
// the configured target. It deliberately does NOT apply the listen-peer filter,
// so it serves both auto-forwarding (after a listen check) and reaction triggers
// (which forward any message the user reacts to, on any peer).
func (w *Watcher) enqueueForwardMessage(ctx context.Context, e tg.Entities, msg *tg.Message) error {
	if w.forward == nil || !w.forward.enabled || msg == nil || msg.Out {
		return nil
	}
	peerID := tutil.GetPeerID(msg.PeerID)

	key := forwardDedupeKey(peerID, msg)
	if w.forward.dedupe.Seen(key) {
		logctx.From(ctx).Debug("Duplicate forward update skipped",
			zap.String("key", key),
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return nil
	}
	accepted := false
	defer func() {
		if !accepted {
			w.forward.dedupe.Forget(key)
		}
	}()

	// Resolve the source peer so the manager caches its access hash; the queue
	// worker re-resolves it by id when it later runs the job.
	from, err := w.resolveForwardPeer(ctx, e, msg.PeerID, peerID)
	if err != nil {
		w.notify(ctx, "监听转发失败：无法解析来源。\n来源：%d\n消息：%d\n错误：%v", peerID, msg.ID, err)
		return err
	}
	originName := from.VisibleName()

	logctx.From(ctx).Info("Enqueuing message for forward",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Int64("target_id", w.forward.target.ID()))

	if _, err := w.opts.ForwardQueue.EnqueueMessage(ctx, peerID, msg.ID, originName,
		w.opts.ForwardTarget, w.forward.target.VisibleName(),
		appforward.ConfigModeName(w.forward.mode), w.opts.ForwardSilent); err != nil {
		notifyCtx := context.WithoutCancel(ctx)
		logctx.From(notifyCtx).Error("Enqueue forward failed",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID),
			zap.Error(err))
		w.notify(notifyCtx, "监听转发入队失败。\n来源：%d\n消息：%d\n错误：%v", peerID, msg.ID, err)
		return err
	}
	accepted = true
	return nil
}

func forwardDedupeKey(peerID int64, msg *tg.Message) string {
	if groupedID, ok := msg.GetGroupedID(); ok && groupedID != 0 {
		return fmt.Sprintf("forward:%d:g:%d", peerID, groupedID)
	}
	return fmt.Sprintf("forward:%d:m:%d", peerID, msg.ID)
}

func (w *Watcher) processForwardIntent(ctx context.Context, request types.ForwardIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	peerID, msgID := request.PeerID, request.MessageID
	inputPeer := protocolPeer(request.Peer)
	if inputPeer == nil {
		var err error
		inputPeer, err = w.resolvePeer(ctx, peerID)
		if err != nil {
			w.notify(ctx, "监听转发（回应触发）失败：无法解析来源。\n来源：%d\n消息：%d\n错误：%v", peerID, msgID, err)
			return err
		}
	}
	msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), inputPeer, msgID)
	if err != nil {
		w.notify(ctx, "监听转发（回应触发）失败：无法获取消息。\n来源：%d\n消息：%d\n错误：%v", peerID, msgID, err)
		return err
	}
	if msg == nil || msg.Out || !w.opts.Forward {
		return nil
	}
	// Cache the event's access hash before enqueue resolves the fetched message.
	if _, err := w.manager.FromInputPeer(ctx, inputPeer); err != nil {
		return err
	}
	if request.Automatic {
		var result error
		destinations := w.ruleDestinations(ctx, msg.PeerID)
		for _, destination := range destinations {
			source := plainPeer(inputPeer)
			groupedID, _ := msg.GetGroupedID()
			_, err := w.opts.ForwardQueue.EnqueueRouted(ctx, source, msg.ID, groupedID, string(chatReference(msg.PeerID)), destination)
			result = errors.Join(result, err)
		}
		// Explicit rules replace the legacy single-target route for this source.
		if len(destinations) == 0 && w.forward != nil {
			if _, legacy := w.forward.listen[peerID]; legacy {
				result = errors.Join(result, w.enqueueForwardMessage(ctx, tg.Entities{}, msg))
			}
		}
		return result
	}
	// Reaction triggers forward the reacted message regardless of the listen set.
	return w.enqueueForwardMessage(ctx, tg.Entities{}, msg)
}

func (w *Watcher) publishForwardIntent(ctx context.Context, entities tg.Entities, peer tg.PeerClass, peerID int64, messageID int) error {
	return w.publishForward(ctx, entities, peer, peerID, messageID, false)
}

func (w *Watcher) publishAutomaticForwardIntent(ctx context.Context, entities tg.Entities, peer tg.PeerClass, peerID int64, messageID int) error {
	return w.publishForward(ctx, entities, peer, peerID, messageID, true)
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

func chatReference(peer tg.PeerClass) types.ChatRef {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return types.ChatRef(fmt.Sprintf("user:%d", p.UserID))
	case *tg.PeerChat:
		return types.ChatRef(fmt.Sprintf("chat:%d", p.ChatID))
	case *tg.PeerChannel:
		return types.ChatRef(fmt.Sprintf("channel:%d", p.ChannelID))
	default:
		return ""
	}
}

func (w *Watcher) ruleDestinations(ctx context.Context, peer tg.PeerClass) []types.ForwardDestination {
	if w.opts.ForwardRules == nil {
		return nil
	}
	return w.opts.ForwardRules.Destinations(ctx, chatReference(peer))
}
