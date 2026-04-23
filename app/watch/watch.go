package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/filterMap"
	"github.com/iyear/tdl/pkg/kv"
	pkgtclient "github.com/iyear/tdl/pkg/tclient"
	"github.com/iyear/tdl/pkg/tplfunc"
	"github.com/iyear/tdl/pkg/utils"
)

type Options struct {
	Dir      string
	Template string
	SkipSame bool
	Threads  int
	Include  []string
	Exclude  []string
	Notify   NotifyFunc
}

type NotifyFunc func(ctx context.Context, text string)

const defaultFileTemplate = "{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}"

func DefaultOptions(cfg *config.Config) Options {
	if cfg == nil {
		cfg = config.Get()
	}

	return Options{
		Dir:      cfg.DownloadDir,
		Template: defaultFileTemplate,
		Threads:  cfg.Threads,
		Include:  append([]string(nil), cfg.Include...),
		Exclude:  append([]string(nil), cfg.Exclude...),
	}
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

type downloadJob struct {
	peer   tg.InputPeerClass
	msgID  int
	peerID int64
	link   string
}

const aria2ConnectRetryInterval = 10 * time.Second

type aria2ConcurrentDownloadSetter interface {
	SetMaxConcurrentDownloads(ctx context.Context, limit int) error
}

type fileTask struct {
	msg    *tg.Message
	media  *tmedia.Media
	peer   tg.InputPeerClass
	peerID int64
}

type fileCollection struct {
	files   []fileTask
	total   int
	skipped int
}

type preparedFileTask struct {
	file     fileTask
	fileName string
	dir      string
	out      string
	fullPath string
}

type Watcher struct {
	opts    Options
	pool    dcpool.Pool
	manager *peers.Manager
	tpl     *template.Template
	runtime *watchRuntime

	dedup   sync.Map
	jobCh   chan downloadJob
	include map[string]struct{}
	exclude map[string]struct{}
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.Get()
	if err := validateWatchConfig(cfg); err != nil {
		return err
	}

	tpl, err := template.New("watch").
		Funcs(tplfunc.FuncMap(tplfunc.All...)).
		Parse(opts.Template)
	if err != nil {
		return errors.Wrap(err, "parse template")
	}

	kvd, err := kv.From(ctx).Open(cfg.Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runtime := newWatchRuntime(cfg, kvd, logctx.From(ctx))
	if err := waitForAria2(ctx, runtime.aria2, cfg.Limit, aria2ConnectRetryInterval, logctx.From(ctx)); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return errors.Wrap(err, "configure aria2 max concurrent downloads")
	}
	logctx.From(ctx).Info("Configured aria2 max concurrent downloads",
		zap.Int("limit", cfg.Limit))

	proxyErrCh := make(chan error, 1)
	go func() {
		if err := runtime.proxy.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case proxyErrCh <- err:
			default:
			}
			cancel()
		}
	}()

	color.Green("👀 Watching for reactions... Press Ctrl+C to stop")
	color.Green("   HTTP listen: %s", cfg.HTTP.Listen)
	color.Green("   Public base URL: %s", cfg.HTTP.PublicBaseURL)
	color.Green("   aria2 RPC: %s", cfg.Aria2.RPCURL)
	outputDir := effectiveOutputDir(cfg, opts)
	if outputDir == "" {
		outputDir = "(aria2 default)"
	}
	color.Green("   Output dir: %s", outputDir)
	color.Green("   Max concurrent downloads: %d", cfg.Limit)
	warnPublicBaseURL(cfg.HTTP.PublicBaseURL)

	reconnectDelay := time.Duration(cfg.ReconnectTimeout) * time.Second
	if reconnectDelay <= 0 {
		reconnectDelay = 5 * time.Second
	}
	var pausedAria2GIDs []string

	for {
		select {
		case err := <-proxyErrCh:
			return errors.Wrap(err, "start http proxy")
		default:
		}

		if ctx.Err() != nil {
			return nil
		}

		resumed, err := runOnce(ctx, opts, tpl, kvd, reconnectDelay, runtime, pausedAria2GIDs)
		if resumed {
			pausedAria2GIDs = nil
		}
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}

		color.Yellow("⚠️ Watcher disconnected: %v", err)
		newPausedGIDs, pauseErr := suspendTDLAria2TasksForReconnect(ctx, runtime.aria2, runtime.aria2Tasks, cfg.HTTP.PublicBaseURL, logctx.From(ctx))
		if pauseErr != nil {
			if errors.Is(pauseErr, context.Canceled) {
				return nil
			}
			return errors.Wrap(pauseErr, "pause aria2 tasks before reconnect")
		}
		pausedAria2GIDs = mergeUniqueGIDs(pausedAria2GIDs, newPausedGIDs)
		if len(newPausedGIDs) > 0 {
			color.Yellow("⏸ Paused %d tdl aria2 task(s) before reconnect", len(newPausedGIDs))
		}
		color.Yellow("🔄 Reconnecting in %v...", reconnectDelay)

		select {
		case err := <-proxyErrCh:
			return errors.Wrap(err, "start http proxy")
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func runOnce(ctx context.Context, opts Options, tpl *template.Template, kvd storage.Storage, reconnectDelay time.Duration, runtime *watchRuntime, pausedAria2GIDs []string) (resumed bool, rerr error) {
	cfg := config.Get()

	o := pkgtclient.Options{
		KV:               kvd,
		Proxy:            cfg.Proxy,
		NTP:              cfg.NTP,
		ReconnectTimeout: reconnectDelay,
	}

	d := tg.NewUpdateDispatcher()
	w := &Watcher{
		opts:    opts,
		tpl:     tpl,
		runtime: runtime,
		jobCh:   make(chan downloadJob, 100),
		include: filterMap.New(opts.Include, addPrefixDot),
		exclude: filterMap.New(opts.Exclude, addPrefixDot),
	}

	d.OnMessageReactions(w.onReaction)
	d.OnEditMessage(w.onEditMessage)
	d.OnEditChannelMessage(w.onEditChannelMessage)
	d.OnFallback(func(ctx context.Context, e tg.Entities, update tg.UpdateClass) error {
		updateType := fmt.Sprintf("%T", update)
		logctx.From(ctx).Info("Unhandled update received",
			zap.String("type", updateType),
			zap.Bool("entities_short", e.Short))
		return nil
	})

	updatesMgr := updates.New(updates.Config{
		Handler: &loggingUpdateHandler{
			inner: d,
		},
		Logger: logctx.From(ctx).Named("updates"),
	})
	o.UpdateHandler = updatesMgr

	client, err := pkgtclient.New(ctx, o, false)
	if err != nil {
		return false, errors.Wrap(err, "create client")
	}

	err = tclient.RunWithAuth(ctx, client, func(ctx context.Context) error {
		pool := dcpool.NewPool(client,
			int64(cfg.PoolSize),
			tclient.NewDefaultMiddlewares(ctx, reconnectDelay)...)
		defer multierr.AppendInvoke(&rerr, multierr.Close(pool))
		defer runtime.pools.Set(nil)

		runtime.pools.Set(pool)

		w.pool = pool
		w.manager = peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))

		self, err := client.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self user")
		}
		if len(pausedAria2GIDs) > 0 {
			if err := resumeTDLAria2Tasks(ctx, runtime.aria2, pausedAria2GIDs, logctx.From(ctx)); err != nil {
				return errors.Wrap(err, "resume paused aria2 tasks")
			}
			resumed = true
			color.Green("▶ Resumed %d tdl aria2 task(s) after reconnect", len(uniqueGIDs(pausedAria2GIDs)))
		}
		go func() {
			if err := updatesMgr.Run(ctx, pool.Default(ctx), self.ID, updates.AuthOptions{
				IsBot: false,
			}); err != nil && !errors.Is(err, context.Canceled) {
				logctx.From(ctx).Error("Updates manager stopped with error", zap.Error(err))
			}
		}()

		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(cfg.Limit)
		go w.dispatcher(egCtx, eg)

		<-ctx.Done()
		color.Yellow("⏹ Stopping watcher...")

		close(w.jobCh)
		if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			logctx.From(ctx).Error("Submission goroutine error", zap.Error(err))
		}

		return nil
	})
	return resumed, err
}

func waitForAria2(ctx context.Context, client aria2ConcurrentDownloadSetter, limit int, retryInterval time.Duration, logger *zap.Logger) error {
	if retryInterval <= 0 {
		retryInterval = aria2ConnectRetryInterval
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	for {
		err := client.SetMaxConcurrentDownloads(ctx, limit)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || !isAria2ConnectionError(err) {
			return err
		}

		logger.Warn("Cannot connect to aria2 RPC, retrying",
			zap.Duration("retry_interval", retryInterval),
			zap.Error(err))
		color.Yellow("⚠️ Cannot connect to aria2 RPC: %v", err)
		color.Yellow("🔄 Retrying in %v...", retryInterval)

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func validateWatchConfig(cfg *config.Config) error {
	if cfg.HTTP.Listen == "" {
		return errors.New("http.listen is empty")
	}
	if cfg.HTTP.PublicBaseURL == "" {
		return errors.New("http.public_base_url is empty, please set it in config.json")
	}
	if cfg.Aria2.RPCURL == "" {
		return errors.New("aria2.rpc_url is empty, please set it in config.json")
	}
	if cfg.Limit < 1 {
		return errors.New("limit must be greater than 0")
	}
	return nil
}

func warnPublicBaseURL(base string) {
	u, err := url.Parse(base)
	if err != nil {
		return
	}

	switch u.Hostname() {
	case "0.0.0.0", "::":
		color.Yellow("⚠️ http.public_base_url uses %s; aria2 usually cannot download from this address directly", u.Hostname())
	case "localhost":
		color.Yellow("⚠️ http.public_base_url uses localhost; this only works when aria2 runs on the same machine and network namespace")
	default:
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			color.Yellow("⚠️ http.public_base_url uses loopback address %s; this only works when aria2 runs on the same machine and network namespace", u.Hostname())
		}
	}
}

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

	isMine := w.isMyMessageReactions(ctx, &update.Reactions, peerID, update.MsgID)
	if !isMine {
		logctx.From(ctx).Info("Reaction is not mine, skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		return nil
	}

	inputPeer := w.peerToInputPeer(update.Peer, e)
	key := fmt.Sprintf("%d:%d", peerID, update.MsgID)
	if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
		logctx.From(ctx).Info("Duplicate reaction, skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		return nil
	}

	msgLink := w.generateMessageLink(update.Peer, update.MsgID)
	logctx.From(ctx).Info("My reaction detected, queuing submission",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", update.MsgID),
		zap.Bool("input_peer_nil", inputPeer == nil),
		zap.String("message_link", msgLink))

	color.Cyan("📌 My reaction on %d/%d, queueing submission...", peerID, update.MsgID)
	color.Green("🔗 Message link: %s", msgLink)

	select {
	case w.jobCh <- downloadJob{peer: inputPeer, msgID: update.MsgID, peerID: peerID, link: msgLink}:
	default:
		logctx.From(ctx).Warn("Submission queue full, dropping job",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		w.dedup.Delete(key)
		w.notify(ctx, "下载队列已满，已丢弃本次触发。\n消息：%s", msgLink)
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

	if !w.isMyMessageReactions(ctx, &msg.Reactions, peerID, msg.ID) {
		logctx.From(ctx).Info("Reaction via EditMessage is not mine, skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return nil
	}

	inputPeer := w.peerToInputPeer(msg.PeerID, e)
	key := fmt.Sprintf("%d:%d", peerID, msg.ID)
	if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
		logctx.From(ctx).Info("Duplicate reaction (via EditMessage), skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return nil
	}

	msgLink := w.generateMessageLink(msg.PeerID, msg.ID)
	logctx.From(ctx).Info("My reaction detected via EditMessage, queuing submission",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Bool("input_peer_nil", inputPeer == nil),
		zap.String("message_link", msgLink))

	color.Cyan("📌 My reaction on %d/%d (via edit), queueing submission...", peerID, msg.ID)
	color.Green("🔗 Message link: %s", msgLink)

	select {
	case w.jobCh <- downloadJob{peer: inputPeer, msgID: msg.ID, peerID: peerID, link: msgLink}:
	default:
		logctx.From(ctx).Warn("Submission queue full, dropping job",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		w.dedup.Delete(key)
		w.notify(ctx, "下载队列已满，已丢弃本次触发。\n消息：%s", msgLink)
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

	if recent, ok := reactions.GetRecentReactions(); ok {
		for _, r := range recent {
			emoji := reactionEmoji(r.Reaction)
			logctx.From(ctx).Debug("Checking RecentReaction",
				zap.Bool("my", r.My),
				zap.String("emoji", emoji),
				zap.Int64("peer_id", tutil.GetPeerID(r.PeerID)))
			if r.My {
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
			logctx.From(ctx).Info("Found my reaction via ChosenOrder",
				zap.String("emoji", emoji),
				zap.Int("chosen_order", chosenOrder),
				zap.Int64("peer_id", peerID),
				zap.Int("msg_id", msgID))
			return true
		}
	}

	logctx.From(ctx).Info("Reaction is NOT mine (both methods failed)",
		zap.Int("results_count", len(reactions.Results)),
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msgID))
	return false
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
		return fmt.Sprintf("(private chat user_id=%d msg_id=%d)", p.UserID, msgID)
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

func (w *Watcher) dispatcher(ctx context.Context, eg *errgroup.Group) {
	for job := range w.jobCh {
		if ctx.Err() != nil {
			return
		}

		logctx.From(ctx).Info("Dispatcher processing job",
			zap.Int64("peer_id", job.peerID),
			zap.Int("msg_id", job.msgID),
			zap.Bool("peer_nil", job.peer == nil))

		peer := job.peer
		if peer == nil {
			resolved, err := w.resolvePeer(ctx, job.peerID)
			if err != nil {
				logctx.From(ctx).Error("Failed to resolve peer",
					zap.Int64("peer_id", job.peerID),
					zap.Error(err))
				color.Red("❌ Cannot resolve peer %d: %v", job.peerID, err)
				w.notify(ctx, "无法解析消息来源，下载任务未提交。\n消息：%s\nPeer: %d\n错误：%v", job.link, job.peerID, err)
				continue
			}
			peer = resolved
		}

		msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), peer, job.msgID)
		if err != nil {
			logctx.From(ctx).Error("Failed to get message",
				zap.Int("msg_id", job.msgID),
				zap.Error(err))
			color.Red("❌ Cannot get message %d: %v", job.msgID, err)
			w.notify(ctx, "无法获取触发消息，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, job.msgID, err)
			continue
		}

		collection, err := w.collectFiles(ctx, msg, peer, job.peerID)
		if err != nil {
			color.Red("❌ Failed to collect files for msg %d: %v", job.msgID, err)
			w.notify(ctx, "解析消息媒体失败，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, job.msgID, err)
			continue
		}

		prepared := make([]preparedFileTask, 0, len(collection.files))
		for _, f := range collection.files {
			task, skip, err := w.prepareSingle(ctx, f)
			if err != nil {
				color.Red("❌ Failed to prepare file for msg %d: %v", f.msg.ID, err)
				w.notify(ctx, "准备下载任务失败，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, f.msg.ID, err)
				continue
			}
			if skip {
				collection.skipped++
				continue
			}
			prepared = append(prepared, task)
		}

		w.notify(ctx, "监听到了新增回应触发。\n链接：%s\n文件总数：%d\n需要下载：%d\n跳过：%d", job.link, collection.total, len(prepared), collection.skipped)
		if len(prepared) == 0 {
			continue
		}

		for _, f := range prepared {
			f := f
			eg.Go(func() error {
				if err := w.submitSingle(ctx, f); err != nil {
					if !errors.Is(err, context.Canceled) {
						logctx.From(ctx).Error("Submission failed",
							zap.Int("msg_id", f.file.msg.ID),
							zap.String("name", f.file.media.Name),
							zap.Error(err))
						color.Red("❌ Submission failed: msg %d (%s): %v", f.file.msg.ID, f.file.media.Name, err)
						w.notify(ctx, "提交到 aria2 失败。\n文件：%s\n消息 ID: %d\n错误：%v", f.file.media.Name, f.file.msg.ID, err)
					}
				}
				return nil
			})
		}
	}
}

func (w *Watcher) collectFiles(ctx context.Context, msg *tg.Message, peer tg.InputPeerClass, peerID int64) (fileCollection, error) {
	var collection fileCollection

	if groupedID, ok := msg.GetGroupedID(); ok {
		logctx.From(ctx).Info("Grouped message detected",
			zap.Int("msg_id", msg.ID),
			zap.Int64("grouped_id", groupedID))

		from, err := w.manager.FromInputPeer(ctx, peer)
		if err != nil {
			return fileCollection{}, errors.Wrap(err, "resolve input peer")
		}

		grouped, err := tutil.GetGroupedMessages(ctx, w.pool.Default(ctx), from.InputPeer(), msg)
		if err != nil {
			return fileCollection{}, errors.Wrap(err, "get grouped messages")
		}

		color.Cyan("📁 Album detected: %d items, queueing all...", len(grouped))
		for _, m := range grouped {
			media, ok := tmedia.GetMedia(m)
			if !ok {
				continue
			}
			collection.total++
			if !w.matchFilter(media.Name) {
				color.Yellow("⏭ Skipping filtered (album): %s", media.Name)
				collection.skipped++
				continue
			}
			collection.files = append(collection.files, fileTask{msg: m, media: media, peer: peer, peerID: peerID})
		}

		return collection, nil
	}

	media, ok := tmedia.GetMedia(msg)
	if !ok {
		color.Yellow("⚠️ Message %d has no media, skipping", msg.ID)
		return collection, nil
	}
	collection.total++
	if !w.matchFilter(media.Name) {
		color.Yellow("⏭ Skipping filtered: %s", media.Name)
		collection.skipped++
		return collection, nil
	}

	collection.files = append(collection.files, fileTask{msg: msg, media: media, peer: peer, peerID: peerID})
	return collection, nil
}

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

func (w *Watcher) resolvePeer(ctx context.Context, peerID int64) (tg.InputPeerClass, error) {
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

func (w *Watcher) prepareSingle(ctx context.Context, file fileTask) (preparedFileTask, bool, error) {
	dialogID := tutil.GetInputPeerID(file.peer)
	fileName, err := w.renderFileName(dialogID, file.msg, file.media)
	if err != nil {
		return preparedFileTask{}, false, err
	}

	dir, out, fullPath := resolveTargetPath(effectiveOutputDir(config.Get(), w.opts), fileName)
	if w.opts.SkipSame && dir != "" {
		if stat, statErr := os.Stat(fullPath); statErr == nil && stat.Size() == file.media.Size {
			color.Yellow("⏭ Skipping existing: %s", fullPath)
			return preparedFileTask{}, true, nil
		}
	}

	return preparedFileTask{
		file:     file,
		fileName: fileName,
		dir:      dir,
		out:      out,
		fullPath: fullPath,
	}, false, nil
}

func (w *Watcher) submitSingle(ctx context.Context, prepared preparedFileTask) error {
	file := prepared.file
	task, err := w.runtime.proxy.NewTask(ctx, file.peerID, file.msg.ID, file.peer, prepared.fileName, file.media.Size, file.media)
	if err != nil {
		return errors.Wrap(err, "register download task")
	}

	downloadURL, err := w.runtime.proxy.BuildURL(task.ID)
	if err != nil {
		return errors.Wrap(err, "build download url")
	}

	gid, err := w.runtime.aria2.AddURI(ctx, downloadURL, aria2AddURIOptions{
		Dir:         prepared.dir,
		Out:         prepared.out,
		Connections: w.opts.Threads,
	})
	if err != nil {
		return errors.Wrap(err, "submit to aria2")
	}
	if err := w.runtime.aria2Tasks.Add(ctx, aria2TaskRecord{
		GID:         gid,
		TaskID:      task.ID,
		DownloadURL: downloadURL,
		CreatedAt:   time.Now(),
	}); err != nil {
		logctx.From(ctx).Warn("Failed to register aria2 task",
			zap.String("gid", gid),
			zap.String("task_id", task.ID),
			zap.String("download_url", downloadURL),
			zap.Error(err))
	}

	logctx.From(ctx).Info("Submitted aria2 task",
		zap.Int64("peer_id", file.peerID),
		zap.Int("msg_id", file.msg.ID),
		zap.String("file_name", prepared.fileName),
		zap.String("target_path", prepared.fullPath),
		zap.String("download_url", downloadURL),
		zap.String("gid", gid))

	color.Green("🚀 Submitted to aria2: msg %d -> %s", file.msg.ID, prepared.fullPath)
	color.Green("   URL: %s", downloadURL)
	color.Green("   GID: %s", gid)

	return nil
}

func (w *Watcher) notify(ctx context.Context, format string, args ...interface{}) {
	if w.opts.Notify == nil {
		return
	}

	text := fmt.Sprintf(format, args...)
	go w.opts.Notify(context.WithoutCancel(ctx), text)
}

func (w *Watcher) renderFileName(dialogID int64, msg *tg.Message, media *tmedia.Media) (string, error) {
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
		return "", errors.Wrap(err, "execute template")
	}
	return toName.String(), nil
}

func resolveTargetPath(baseDir, renderedName string) (dir, out, fullPath string) {
	cleanName := filepath.Clean(renderedName)
	dir = baseDir

	if subDir := filepath.Dir(cleanName); subDir != "." {
		if dir == "" {
			dir = subDir
		} else {
			dir = filepath.Join(dir, subDir)
		}
	}

	out = filepath.Base(cleanName)
	if dir == "" || dir == "." {
		fullPath = out
		return dir, out, fullPath
	}

	fullPath = filepath.Join(dir, out)
	return dir, out, fullPath
}

func effectiveOutputDir(cfg *config.Config, opts Options) string {
	_ = opts
	return cfg.Aria2.Dir
}

func addPrefixDot(v string) string {
	if v == "" || v[0] == '.' {
		return v
	}
	return "." + v
}

type loggingUpdateHandler struct {
	inner telegram.UpdateHandler
}

func (h *loggingUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
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
