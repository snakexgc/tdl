package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/jedib0t/go-pretty/v6/progress"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/filterMap"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/pkg/prog"
	pkgtclient "github.com/iyear/tdl/pkg/tclient"
	"github.com/iyear/tdl/pkg/tplfunc"
	"github.com/iyear/tdl/pkg/utils"
)

const tempExt = ".tmp"

type Options struct {
	Dir        string
	Template   string
	SkipSame   bool
	RewriteExt bool
	Threads    int
	Include    []string
	Exclude    []string
}

type fileTemplate struct {
	DialogID     int64
	MessageID    int
	MessageDate  int64
	FileName     string
	FileCaption  string
	FileSize     string
	DownloadDate int64
}

// downloadJob represents a single message to be downloaded.
type downloadJob struct {
	peer  tg.InputPeerClass
	msgID int
	// peerID is kept for logging when peer is nil (needs resolution via manager)
	peerID int64
}

// fileTask represents a single file to download, extracted from a message.
// A single message (especially grouped/album) may produce multiple fileTasks.
type fileTask struct {
	msg   *tg.Message
	media *tmedia.Media
	peer  tg.InputPeerClass
}

type Watcher struct {
	opts    Options
	pool    dcpool.Pool
	manager *peers.Manager
	tpl     *template.Template

	// dedup tracks already-processed peerID:msgID pairs
	dedup sync.Map
	jobCh chan downloadJob

	// include/exclude filter maps (extension → struct{})
	include map[string]struct{}
	exclude map[string]struct{}

	// eg controls file-level concurrency via errgroup
	egCtx context.Context
	eg    *errgroup.Group

	// progress bar
	pw       progress.Writer
	progress *watchProgress
}

func Run(ctx context.Context, opts Options) (rerr error) {
	cfg := config.Get()

	// parse template
	tpl, err := template.New("watch").
		Funcs(tplfunc.FuncMap(tplfunc.All...)).
		Parse(opts.Template)
	if err != nil {
		return errors.Wrap(err, "parse template")
	}

	// create tOptions (same as tRun, but we need to set UpdateHandler)
	kvd, err := kv.From(ctx).Open(cfg.Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}

	o := pkgtclient.Options{
		KV:               kvd,
		Proxy:            cfg.Proxy,
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	}

	// create update dispatcher for reaction events
	d := tg.NewUpdateDispatcher()

	// We need a reference to the Watcher before registering handlers,
	// but Watcher isn't created yet. Use a pointer indirection.
	dlProgress := prog.New(utils.Byte.FormatBinaryBytes)
	w := &Watcher{
		opts:     opts,
		tpl:      tpl,
		jobCh:    make(chan downloadJob, 100), // buffered queue for pending downloads
		pw:       dlProgress,
		progress: newWatchProgress(dlProgress),
		include:  filterMap.New(opts.Include, fsutil.AddPrefixDot),
		exclude:  filterMap.New(opts.Exclude, fsutil.AddPrefixDot),
	}

	// register reaction handler (UpdateMessageReactions — used in groups/channels
	// when someone else reacts to YOUR messages, or when the server explicitly
	// sends a reaction update)
	d.OnMessageReactions(w.onReaction)

	// register edit message handlers (UpdateEditMessage/UpdateEditChannelMessage).
	// In private chats, when you add a reaction to someone else's message,
	// Telegram sends UpdateEditMessage instead of UpdateMessageReactions.
	// The message's Reactions field gets updated with the new reaction data.
	d.OnEditMessage(w.onEditMessage)
	d.OnEditChannelMessage(w.onEditChannelMessage)

	// register fallback handler to log all unhandled update types
	d.OnFallback(func(ctx context.Context, e tg.Entities, update tg.UpdateClass) error {
		updateType := fmt.Sprintf("%T", update)
		logctx.From(ctx).Info("Unhandled update received",
			zap.String("type", updateType),
			zap.Bool("entities_short", e.Short))
		return nil
	})

	// Create updates.Manager for proper pts tracking and gap recovery.
	// Without this, Telegram may not push certain updates (like
	// UpdateMessageReactions) because the client doesn't maintain
	// pts state, causing the server to skip updates during pts gaps.
	updatesMgr := updates.New(updates.Config{
		Handler: &loggingUpdateHandler{
			inner: d,
		},
		Logger: logctx.From(ctx).Named("updates"),
	})

	// updates.Manager implements telegram.UpdateHandler, so we pass it directly.
	o.UpdateHandler = updatesMgr

	client, err := pkgtclient.New(ctx, o, false)
	if err != nil {
		return errors.Wrap(err, "create client")
	}

	return tclient.RunWithAuth(ctx, client, func(ctx context.Context) error {
		pool := dcpool.NewPool(client,
			int64(cfg.PoolSize),
			tclient.NewDefaultMiddlewares(ctx, time.Duration(cfg.ReconnectTimeout)*time.Second)...)
		defer multierr.AppendInvoke(&rerr, multierr.Close(pool))

		w.pool = pool
		w.manager = peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))

		// Start the updates manager to enable pts tracking and gap recovery.
		// This is CRITICAL: without calling updatesMgr.Run(), the manager
		// has no internal state and passes updates through as-is (same as
		// not using it). With Run(), it tracks pts/seq/qts and calls
		// updates.getDifference when gaps are detected, ensuring we receive
		// all updates including UpdateMessageReactions.
		self, err := client.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self user")
		}
		go func() {
			if err := updatesMgr.Run(ctx, pool.Default(ctx), self.ID, updates.AuthOptions{
				IsBot: false,
			}); err != nil && !errors.Is(err, context.Canceled) {
				logctx.From(ctx).Error("Updates manager stopped with error", zap.Error(err))
			}
		}()

		// Set up file-level concurrency control using errgroup.
		// FlagLimit controls how many files can download simultaneously.
		limit := cfg.Limit
		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(limit)
		w.eg = eg
		w.egCtx = egCtx

		// Start dispatcher goroutine: reads message-level jobs from jobCh,
		// resolves them into file-level tasks, and submits each file to the errgroup.
		go w.dispatcher(egCtx, eg)

		color.Green("👀 Watching for reactions... Press Ctrl+C to stop")
		color.Green("   Download dir: %s", opts.Dir)
		color.Green("   Max concurrent files: %d", limit)
		color.Green("   Threads per file: %d", opts.Threads)

		// start progress bar rendering (after banner output)
		go dlProgress.Render()

		// ensure download dir exists
		if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
			return errors.Wrap(err, "create download dir")
		}

		// wait for context cancellation (Ctrl+C)
		ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		<-ctx.Done()
		w.pw.Log(color.YellowString("⏹ Stopping watcher..."))

		// close job channel so dispatcher exits, then wait for all file downloads to finish
		close(w.jobCh)
		if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			logctx.From(ctx).Error("Download goroutine error", zap.Error(err))
		}

		// stop progress bar rendering
		prog.Wait(ctx, dlProgress)

		return nil
	})
}

// onReaction is the callback registered with tg.UpdateDispatcher.
// It fires whenever a reaction update is received from Telegram.
func (w *Watcher) onReaction(ctx context.Context, e tg.Entities, update *tg.UpdateMessageReactions) error {
	peerID := tutil.GetPeerID(update.Peer)

	// Determine peer type for logging
	peerType := "unknown"
	switch update.Peer.(type) {
	case *tg.PeerUser:
		peerType = "user"
	case *tg.PeerChat:
		peerType = "chat"
	case *tg.PeerChannel:
		peerType = "channel"
	}

	// Log every reaction update for debugging
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

	w.pw.Log(color.CyanString("🔔 Reaction update: type=%s peer=%d msg=%d min=%v results=%d",
		peerType, peerID, update.MsgID, update.Reactions.Min, len(update.Reactions.Results)))

	isMine := w.isMyReaction(ctx, update)
	if !isMine {
		logctx.From(ctx).Info("Reaction is not mine, skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		return nil
	}

	inputPeer := w.peerToInputPeer(update.Peer, e)
	// inputPeer can be nil if Entities doesn't have the access hash.
	// The download worker will try to resolve it via peers.Manager.

	// dedup check
	key := fmt.Sprintf("%d:%d", peerID, update.MsgID)
	if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
		logctx.From(ctx).Info("Duplicate reaction, skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		return nil // already queued or downloaded
	}

	// Generate message link
	msgLink := w.generateMessageLink(update.Peer, update.MsgID)

	logctx.From(ctx).Info("My reaction detected, queuing download",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", update.MsgID),
		zap.Bool("input_peer_nil", inputPeer == nil),
		zap.String("message_link", msgLink))

	w.pw.Log(color.CyanString("📌 My reaction on %d/%d, queuing download...", peerID, update.MsgID))
	w.pw.Log(color.GreenString("🔗 Message link: %s", msgLink))

	// non-blocking send to job channel
	select {
	case w.jobCh <- downloadJob{peer: inputPeer, msgID: update.MsgID, peerID: peerID}:
	default:
		logctx.From(ctx).Warn("Download queue full, dropping job",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		w.dedup.Delete(key) // allow retry later
	}

	return nil
}

// onEditMessage handles UpdateEditMessage events.
// In private chats, when you add a reaction to someone else's message,
// Telegram sends UpdateEditMessage instead of UpdateMessageReactions.
// The message's Reactions field gets updated with the new reaction data.
func (w *Watcher) onEditMessage(ctx context.Context, e tg.Entities, update *tg.UpdateEditMessage) error {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}
	return w.onEditMessageReaction(ctx, e, msg)
}

// onEditChannelMessage handles UpdateEditChannelMessage events.
// In channels, reaction changes may also come as edit messages.
func (w *Watcher) onEditChannelMessage(ctx context.Context, e tg.Entities, update *tg.UpdateEditChannelMessage) error {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		return nil
	}
	return w.onEditMessageReaction(ctx, e, msg)
}

// onEditMessageReaction processes reaction data found in an edited message.
// This is called by onEditMessage and onEditChannelMessage when a message
// edit contains reaction updates (which happens in private chats when you
// add a reaction — Telegram sends UpdateEditMessage, not UpdateMessageReactions).
func (w *Watcher) onEditMessageReaction(ctx context.Context, e tg.Entities, msg *tg.Message) error {
	// Check if the message has any reaction results.
	// When a reaction is added, Results will be non-nil and non-empty.
	// When a reaction is removed, Results may be nil or empty.
	if msg.Reactions.GetResults() == nil || len(msg.Reactions.Results) == 0 {
		logctx.From(ctx).Debug("EditMessage has no reactions, skipping",
			zap.Int("msg_id", msg.ID))
		return nil
	}

	peerID := tutil.GetPeerID(msg.PeerID)

	// Log reaction details from edited message
	reactionsJSON, _ := json.Marshal(msg.Reactions)
	logctx.From(ctx).Info("Reaction detected via EditMessage",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Bool("reactions_min", msg.Reactions.Min),
		zap.Int("results_count", len(msg.Reactions.Results)),
		zap.String("reactions_json", string(reactionsJSON)))

	w.pw.Log(color.CyanString("🔔 Reaction via EditMessage: peer=%d msg=%d min=%v results=%d",
		peerID, msg.ID, msg.Reactions.Min, len(msg.Reactions.Results)))

	inputPeer := w.peerToInputPeer(msg.PeerID, e)

	// dedup check
	key := fmt.Sprintf("%d:%d", peerID, msg.ID)
	if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
		logctx.From(ctx).Info("Duplicate reaction (via EditMessage), skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return nil
	}

	// Generate message link
	msgLink := w.generateMessageLink(msg.PeerID, msg.ID)

	logctx.From(ctx).Info("Reaction detected via EditMessage, queuing download",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Bool("input_peer_nil", inputPeer == nil),
		zap.String("message_link", msgLink))

	w.pw.Log(color.CyanString("📌 Reaction on %d/%d (via edit), queuing download...", peerID, msg.ID))
	w.pw.Log(color.GreenString("🔗 Message link: %s", msgLink))

	// non-blocking send to job channel
	select {
	case w.jobCh <- downloadJob{peer: inputPeer, msgID: msg.ID, peerID: peerID}:
	default:
		logctx.From(ctx).Warn("Download queue full, dropping job",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		w.dedup.Delete(key) // allow retry later
	}

	return nil
}

// isMyReaction checks whether the current user made any of the reactions.
// When MessageReactions.Min=true, the server sends a compact version that
// omits per-user details (My, ChosenOrder). In that case we accept the
// reaction as "mine" to avoid silently dropping events. The user can add
// a --reaction filter later for precise control.
func (w *Watcher) isMyReaction(ctx context.Context, update *tg.UpdateMessageReactions) bool {
	if update.Reactions.Min {
		// Min=true means the server omitted per-user details.
		// We cannot reliably determine if this is our reaction,
		// so we accept it (conservative approach: don't miss downloads).
		logctx.From(ctx).Info("Reactions.Min=true, accepting as mine (cannot determine ownership)")
		return true
	}

	// method 1: check RecentReactions for My == true
	if recent, ok := update.Reactions.GetRecentReactions(); ok {
		for _, r := range recent {
			emoji := reactionEmoji(r.Reaction)
			logctx.From(ctx).Debug("Checking RecentReaction",
				zap.Bool("my", r.My),
				zap.String("emoji", emoji),
				zap.Int64("peer_id", tutil.GetPeerID(r.PeerID)))
			if r.My {
				logctx.From(ctx).Info("Found my reaction via RecentReactions",
					zap.String("emoji", emoji))
				return true
			}
		}
		logctx.From(ctx).Debug("No My=true in RecentReactions",
			zap.Int("count", len(recent)))
	}

	// method 2: fallback to Results with ChosenOrder set
	for _, rc := range update.Reactions.Results {
		emoji := reactionEmoji(rc.Reaction)
		chosenOrder, ok := rc.GetChosenOrder()
		logctx.From(ctx).Debug("Checking Result ChosenOrder",
			zap.String("emoji", emoji),
			zap.Int("count", rc.Count),
			zap.Bool("chosen", ok),
			zap.Int("chosen_order", chosenOrder))
		if ok {
			logctx.From(ctx).Info("Found my reaction via ChosenOrder",
				zap.String("emoji", emoji),
				zap.Int("chosen_order", chosenOrder))
			return true
		}
	}

	logctx.From(ctx).Info("Reaction is NOT mine (both methods failed)",
		zap.Int("results_count", len(update.Reactions.Results)))
	return false
}

// reactionEmoji extracts the emoji string from a ReactionClass for logging.
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

// generateMessageLink generates a Telegram message link.
// For private channels/groups: https://t.me/c/<channel_id>/<msg_id>
// For users: returns empty string (no public link available)
func (w *Watcher) generateMessageLink(peer tg.PeerClass, msgID int) string {
	switch p := peer.(type) {
	case *tg.PeerChannel:
		// For channels, use the format https://t.me/c/<channel_id>/<msg_id>
		// Note: This works for both public and private channels when accessed by the user
		return fmt.Sprintf("https://t.me/c/%d/%d", p.ChannelID, msgID)
	case *tg.PeerChat:
		// For groups, use the same format
		return fmt.Sprintf("https://t.me/c/%d/%d", p.ChatID, msgID)
	case *tg.PeerUser:
		// For private chats, no public link available
		// Return a descriptive string with user ID and message ID
		return fmt.Sprintf("(private chat user_id=%d msg_id=%d)", p.UserID, msgID)
	default:
		return fmt.Sprintf("(unknown peer type: %T, msg_id=%d)", peer, msgID)
	}
}

// peerToInputPeer converts a PeerClass to InputPeerClass using Entities for access hash.
// Returns nil if the peer cannot be resolved.
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

// dispatcher reads message-level jobs from jobCh, resolves each message into
// one or more file-level tasks, and submits them to the errgroup for concurrent download.
// FlagLimit (via eg.SetLimit) controls how many files download simultaneously.
func (w *Watcher) dispatcher(ctx context.Context, eg *errgroup.Group) {
	for job := range w.jobCh {
		// Check if context is already cancelled before processing
		if ctx.Err() != nil {
			return
		}

		logctx.From(ctx).Info("Dispatcher processing job",
			zap.Int64("peer_id", job.peerID),
			zap.Int("msg_id", job.msgID),
			zap.Bool("peer_nil", job.peer == nil))

		peer := job.peer
		// if peer is nil (Entities didn't have access hash), try to resolve via manager
		if peer == nil {
			logctx.From(ctx).Info("Peer is nil, resolving via manager",
				zap.Int64("peer_id", job.peerID))
			resolved, err := w.resolvePeer(ctx, job.peerID)
			if err != nil {
				logctx.From(ctx).Error("Failed to resolve peer",
					zap.Int64("peer_id", job.peerID),
					zap.Error(err))
				w.pw.Log(color.RedString("❌ Cannot resolve peer %d: %v", job.peerID, err))
				continue
			}
			peer = resolved
			logctx.From(ctx).Info("Peer resolved via manager",
				zap.Int64("peer_id", job.peerID))
		}

		// get the message
		msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), peer, job.msgID)
		if err != nil {
			logctx.From(ctx).Error("Failed to get message",
				zap.Int("msg_id", job.msgID),
				zap.Error(err))
			w.pw.Log(color.RedString("❌ Cannot get message %d: %v", job.msgID, err))
			continue
		}

		// collect all file tasks from this message
		var files []fileTask

		if groupedID, ok := msg.GetGroupedID(); ok {
			// album: fetch all grouped messages and extract media from each
			logctx.From(ctx).Info("Grouped message detected",
				zap.Int("msg_id", job.msgID),
				zap.Int64("grouped_id", groupedID))

			from, err := w.manager.FromInputPeer(ctx, peer)
			if err != nil {
				logctx.From(ctx).Error("Failed to resolve from input peer",
					zap.Error(err))
				w.pw.Log(color.RedString("❌ Cannot resolve peer for grouped msg %d: %v", job.msgID, err))
				continue
			}

			grouped, err := tutil.GetGroupedMessages(ctx, w.pool.Default(ctx), from.InputPeer(), msg)
			if err != nil {
				logctx.From(ctx).Error("Failed to get grouped messages",
					zap.Error(err))
				w.pw.Log(color.RedString("❌ Cannot get grouped messages for msg %d: %v", job.msgID, err))
				continue
			}

			w.pw.Log(color.CyanString("📁 Album detected: %d items, queuing all...", len(grouped)))

			for _, m := range grouped {
				media, ok := tmedia.GetMedia(m)
				if !ok {
					continue // skip messages without media in the group
				}
				if !w.matchFilter(media.Name) {
					w.pw.Log(color.YellowString("⏭ Skipping filtered (album): %s", media.Name))
					continue
				}
				files = append(files, fileTask{msg: m, media: media, peer: peer})
			}
		} else {
			// single message
			media, ok := tmedia.GetMedia(msg)
			if !ok {
				w.pw.Log(color.YellowString("⚠️ Message %d has no media, skipping", job.msgID))
				logctx.From(ctx).Info("Message has no media, skipping",
					zap.Int("msg_id", job.msgID))
				continue
			}
			if !w.matchFilter(media.Name) {
				w.pw.Log(color.YellowString("⏭ Skipping filtered: %s", media.Name))
				continue
			}
			files = append(files, fileTask{msg: msg, media: media, peer: peer})
		}

		// submit each file as an independent task to the errgroup
		for _, f := range files {
			f := f // capture for closure
			eg.Go(func() (rerr error) {
				logctx.From(ctx).Info("Starting file download",
					zap.Int("msg_id", f.msg.ID),
					zap.String("name", f.media.Name),
					zap.Int64("size", f.media.Size))

				if err := w.downloadSingle(ctx, f.msg, f.media, f.peer); err != nil {
					if !errors.Is(err, context.Canceled) {
						logctx.From(ctx).Error("File download failed",
							zap.Int("msg_id", f.msg.ID),
							zap.String("name", f.media.Name),
							zap.Error(err))
						w.pw.Log(color.RedString("❌ Download failed: msg %d (%s): %v", f.msg.ID, f.media.Name, err))
					}
					// don't return error — let other files continue
					return nil
				}

				logctx.From(ctx).Info("File download completed",
					zap.Int("msg_id", f.msg.ID),
					zap.String("name", f.media.Name))
				return nil
			})
		}
	}
}

// matchFilter checks if a file name matches the include/exclude filter.
// Returns true if the file should be downloaded, false if it should be skipped.
// Logic matches dl command: include means "only these extensions", exclude means "not these extensions".
func (w *Watcher) matchFilter(name string) bool {
	ext := filepath.Ext(name)
	if len(w.include) > 0 {
		if _, ok := w.include[ext]; !ok {
			return false
		}
	}
	if len(w.exclude) > 0 {
		if _, ok := w.exclude[ext]; ok {
			return false
		}
	}
	return true
}

// resolvePeer tries to resolve a peer ID to InputPeerClass via peers.Manager.
func (w *Watcher) resolvePeer(ctx context.Context, peerID int64) (tg.InputPeerClass, error) {
	// try channel first, then user, then chat
	if p, err := w.manager.ResolveChannelID(ctx, peerID); err == nil {
		return p.InputPeer(), nil
	}
	if p, err := w.manager.ResolveUserID(ctx, peerID); err == nil {
		return p.InputPeer(), nil
	}
	if p, err := w.manager.ResolveChatID(ctx, peerID); err == nil {
		return p.InputPeer(), nil
	}
	return nil, fmt.Errorf("cannot resolve peer %d via manager", peerID)
}

// downloadSingle downloads a single media file from a message.
func (w *Watcher) downloadSingle(ctx context.Context, msg *tg.Message, media *tmedia.Media, peer tg.InputPeerClass) error {
	dialogID := tutil.GetInputPeerID(peer)

	// apply template for file name
	var toName bytes.Buffer
	if err := w.tpl.Execute(&toName, &fileTemplate{
		DialogID:     dialogID,
		MessageID:    msg.ID,
		MessageDate:  int64(msg.Date),
		FileName:     media.Name,
		FileCaption:  msg.Message,
		FileSize:     utils.Byte.FormatBinaryBytes(media.Size),
		DownloadDate: time.Now().Unix(),
	}); err != nil {
		return errors.Wrap(err, "execute template")
	}

	// check skip same
	if w.opts.SkipSame {
		if stat, statErr := os.Stat(filepath.Join(w.opts.Dir, toName.String())); statErr == nil {
			if fsutil.GetNameWithoutExt(toName.String()) == fsutil.GetNameWithoutExt(stat.Name()) &&
				stat.Size() == media.Size {
				w.pw.Log(color.YellowString("⏭ Skipping existing: %s", toName.String()))
				return nil
			}
		}
	}

	// create temp file
	filename := fmt.Sprintf("%s%s", toName.String(), tempExt)
	path := filepath.Join(w.opts.Dir, filename)

	// ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.Wrap(err, "create dir")
	}

	f, err := os.Create(path)
	if err != nil {
		return errors.Wrap(err, "create file")
	}

	// add progress tracker for this file
	w.progress.OnAdd(msg.ID, filename, media.Size)

	// wrap file with progress-tracking WriterAt
	pwa := newProgressWriteAt(f, w.progress, msg.ID, media.Size)

	// download with progress tracking
	client := w.pool.Client(ctx, media.DC)
	dlErr := downloadFile(ctx, client, media, pwa, w.opts.Threads)

	// close file
	if closeErr := f.Close(); closeErr != nil {
		w.progress.OnDone(msg.ID, closeErr)
		_ = os.Remove(path)
		return errors.Wrap(closeErr, "close file")
	}

	if dlErr != nil {
		w.progress.OnDone(msg.ID, dlErr)
		_ = os.Remove(path)
		return errors.Wrap(dlErr, "download")
	}

	// mark as done in progress
	w.progress.OnDone(msg.ID, nil)

	// post-processing: rewrite ext or rename
	finalName := toName.String()
	if w.opts.RewriteExt {
		finalName = w.rewriteExt(path, finalName)
	}

	finalPath := filepath.Join(w.opts.Dir, finalName)
	if err := os.Rename(path, finalPath); err != nil {
		return errors.Wrap(err, "rename file")
	}

	// set file modification time to media date
	if media.Date > 0 {
		fileTime := time.Unix(media.Date, 0)
		_ = os.Chtimes(finalPath, fileTime, fileTime)
	}

	return nil
}

// loggingUpdateHandler wraps an UpdateHandler and logs every updates batch
// before delegating to the inner handler. This helps diagnose whether
// Telegram is actually pushing reaction updates.
// All output goes to the structured logger only — no direct color printing,
// which would conflict with the go-pretty progress bar rendering.
type loggingUpdateHandler struct {
	inner telegram.UpdateHandler
}

func (h *loggingUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	// Log the type and structure of every update batch
	switch u := updates.(type) {
	case *tg.Updates:
		types := make([]string, 0, len(u.Updates))
		for _, upd := range u.Updates {
			types = append(types, fmt.Sprintf("%T", upd))
		}
		logctx.From(ctx).Debug("Updates batch received",
			zap.String("container", "Updates"),
			zap.Int("count", len(u.Updates)),
			zap.Strings("types", types),
			zap.Int("users", len(u.Users)),
			zap.Int("chats", len(u.Chats)))

	case *tg.UpdatesCombined:
		types := make([]string, 0, len(u.Updates))
		for _, upd := range u.Updates {
			types = append(types, fmt.Sprintf("%T", upd))
		}
		logctx.From(ctx).Debug("UpdatesCombined batch received",
			zap.String("container", "UpdatesCombined"),
			zap.Int("count", len(u.Updates)),
			zap.Strings("types", types),
			zap.Int("users", len(u.Users)),
			zap.Int("chats", len(u.Chats)))

	case *tg.UpdateShort:
		updateType := fmt.Sprintf("%T", u.Update)
		logctx.From(ctx).Debug("UpdateShort received",
			zap.String("type", updateType),
			zap.Int("date", u.Date))

	case *tg.UpdatesTooLong:
		logctx.From(ctx).Debug("UpdatesTooLong received")

	default:
		updateType := fmt.Sprintf("%T", u)
		logctx.From(ctx).Debug("Unknown updates type",
			zap.String("type", updateType))
	}

	return h.inner.Handle(ctx, updates)
}
