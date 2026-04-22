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
}

type fileTask struct {
	msg    *tg.Message
	media  *tmedia.Media
	peer   tg.InputPeerClass
	peerID int64
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
	color.Green("   Max concurrent submissions: %d", cfg.Limit)
	warnPublicBaseURL(cfg.HTTP.PublicBaseURL)

	reconnectDelay := time.Duration(cfg.ReconnectTimeout) * time.Second
	if reconnectDelay <= 0 {
		reconnectDelay = 5 * time.Second
	}

	for {
		select {
		case err := <-proxyErrCh:
			return errors.Wrap(err, "start http proxy")
		default:
		}

		if ctx.Err() != nil {
			return nil
		}

		err := runOnce(ctx, opts, tpl, kvd, reconnectDelay, runtime)
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}

		color.Yellow("⚠️ Watcher disconnected: %v", err)
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

func runOnce(ctx context.Context, opts Options, tpl *template.Template, kvd storage.Storage, reconnectDelay time.Duration, runtime *watchRuntime) (rerr error) {
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
		return errors.Wrap(err, "create client")
	}

	return tclient.RunWithAuth(ctx, client, func(ctx context.Context) error {
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

	isMine := w.isMyReaction(ctx, update)
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
	case w.jobCh <- downloadJob{peer: inputPeer, msgID: update.MsgID, peerID: peerID}:
	default:
		logctx.From(ctx).Warn("Submission queue full, dropping job",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", update.MsgID))
		w.dedup.Delete(key)
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

	inputPeer := w.peerToInputPeer(msg.PeerID, e)
	key := fmt.Sprintf("%d:%d", peerID, msg.ID)
	if _, loaded := w.dedup.LoadOrStore(key, struct{}{}); loaded {
		logctx.From(ctx).Info("Duplicate reaction (via EditMessage), skipping",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		return nil
	}

	msgLink := w.generateMessageLink(msg.PeerID, msg.ID)
	logctx.From(ctx).Info("Reaction detected via EditMessage, queuing submission",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.Bool("input_peer_nil", inputPeer == nil),
		zap.String("message_link", msgLink))

	color.Cyan("📌 Reaction on %d/%d (via edit), queueing submission...", peerID, msg.ID)
	color.Green("🔗 Message link: %s", msgLink)

	select {
	case w.jobCh <- downloadJob{peer: inputPeer, msgID: msg.ID, peerID: peerID}:
	default:
		logctx.From(ctx).Warn("Submission queue full, dropping job",
			zap.Int64("peer_id", peerID),
			zap.Int("msg_id", msg.ID))
		w.dedup.Delete(key)
	}

	return nil
}

func (w *Watcher) isMyReaction(ctx context.Context, update *tg.UpdateMessageReactions) bool {
	if update.Reactions.Min {
		logctx.From(ctx).Info("Reactions.Min=true, accepting as mine (cannot determine ownership)")
		return true
	}

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
	}

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
			continue
		}

		files, err := w.collectFiles(ctx, msg, peer, job.peerID)
		if err != nil {
			color.Red("❌ Failed to collect files for msg %d: %v", job.msgID, err)
			continue
		}

		for _, f := range files {
			f := f
			eg.Go(func() error {
				if err := w.submitSingle(ctx, f.msg, f.media, f.peer, f.peerID); err != nil {
					if !errors.Is(err, context.Canceled) {
						logctx.From(ctx).Error("Submission failed",
							zap.Int("msg_id", f.msg.ID),
							zap.String("name", f.media.Name),
							zap.Error(err))
						color.Red("❌ Submission failed: msg %d (%s): %v", f.msg.ID, f.media.Name, err)
					}
				}
				return nil
			})
		}
	}
}

func (w *Watcher) collectFiles(ctx context.Context, msg *tg.Message, peer tg.InputPeerClass, peerID int64) ([]fileTask, error) {
	var files []fileTask

	if groupedID, ok := msg.GetGroupedID(); ok {
		logctx.From(ctx).Info("Grouped message detected",
			zap.Int("msg_id", msg.ID),
			zap.Int64("grouped_id", groupedID))

		from, err := w.manager.FromInputPeer(ctx, peer)
		if err != nil {
			return nil, errors.Wrap(err, "resolve input peer")
		}

		grouped, err := tutil.GetGroupedMessages(ctx, w.pool.Default(ctx), from.InputPeer(), msg)
		if err != nil {
			return nil, errors.Wrap(err, "get grouped messages")
		}

		color.Cyan("📁 Album detected: %d items, queueing all...", len(grouped))
		for _, m := range grouped {
			media, ok := tmedia.GetMedia(m)
			if !ok {
				continue
			}
			if !w.matchFilter(media.Name) {
				color.Yellow("⏭ Skipping filtered (album): %s", media.Name)
				continue
			}
			files = append(files, fileTask{msg: m, media: media, peer: peer, peerID: peerID})
		}

		return files, nil
	}

	media, ok := tmedia.GetMedia(msg)
	if !ok {
		color.Yellow("⚠️ Message %d has no media, skipping", msg.ID)
		return nil, nil
	}
	if !w.matchFilter(media.Name) {
		color.Yellow("⏭ Skipping filtered: %s", media.Name)
		return nil, nil
	}

	files = append(files, fileTask{msg: msg, media: media, peer: peer, peerID: peerID})
	return files, nil
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

func (w *Watcher) submitSingle(ctx context.Context, msg *tg.Message, media *tmedia.Media, peer tg.InputPeerClass, peerID int64) error {
	dialogID := tutil.GetInputPeerID(peer)
	fileName, err := w.renderFileName(dialogID, msg, media)
	if err != nil {
		return err
	}

	dir, out, fullPath := resolveTargetPath(effectiveOutputDir(config.Get(), w.opts), fileName)
	if w.opts.SkipSame && dir != "" {
		if stat, statErr := os.Stat(fullPath); statErr == nil && stat.Size() == media.Size {
			color.Yellow("⏭ Skipping existing: %s", fullPath)
			return nil
		}
	}

	task, err := w.runtime.proxy.NewTask(ctx, peerID, msg.ID, fileName, media.Size, media)
	if err != nil {
		return errors.Wrap(err, "register download task")
	}

	downloadURL, err := w.runtime.proxy.BuildURL(task.ID)
	if err != nil {
		return errors.Wrap(err, "build download url")
	}

	gid, err := w.runtime.aria2.AddURI(ctx, downloadURL, aria2AddURIOptions{
		Dir:         dir,
		Out:         out,
		Connections: w.opts.Threads,
	})
	if err != nil {
		return errors.Wrap(err, "submit to aria2")
	}

	logctx.From(ctx).Info("Submitted aria2 task",
		zap.Int64("peer_id", peerID),
		zap.Int("msg_id", msg.ID),
		zap.String("file_name", fileName),
		zap.String("target_path", fullPath),
		zap.String("download_url", downloadURL),
		zap.String("gid", gid))

	color.Green("🚀 Submitted to aria2: msg %d -> %s", msg.ID, fullPath)
	color.Green("   URL: %s", downloadURL)
	color.Green("   GID: %s", gid)

	return nil
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
