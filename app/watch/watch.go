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
	"strings"
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

	httpdl "github.com/iyear/tdl/app/http"
	watcharia2 "github.com/iyear/tdl/app/watch/aria2"
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
	Dir                   string
	Template              string
	FilenameMaxLength     int
	SkipSame              bool
	PoolSize              int
	Threads               int
	Limit                 int
	Download              bool
	TriggerReactions      []string
	Include               []string
	Exclude               []string
	FileSizeMB            int64
	Forward                 bool
	ForwardMode             string
	ForwardTarget           string
	ForwardListen           []string
	ForwardListenComments   bool
	ForwardSilent           bool
	ForwardDedupeTTL        time.Duration
	ForwardTriggerReactions []string
	Notify                NotifyFunc
	messageLinks          <-chan messageLinkSubmission
}

type NotifyFunc func(ctx context.Context, text string)

const bytesPerMegabyte int64 = 1024 * 1024

func fileNameConfigTemplate(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.Contains(pattern, "{{") {
		return pattern
	}

	var b strings.Builder
	for _, r := range pattern {
		if r == '&' {
			continue
		}
		if tpl := fileNameTemplateAlias(r); tpl != "" {
			b.WriteString(tpl)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fileNameTemplateAlias(r rune) string {
	switch r {
	case 'F':
		return `{{ .F }}`
	case 'I':
		return `{{ .I }}`
	case 'G':
		return `{{ .G }}`
	case 'P':
		return `{{ .P }}`
	case 'S':
		return `{{ .S }}`
	case 'R':
		return `{{ .R }}`
	case 'A':
		return `{{ .A }}`
	case 'Y':
		return `{{ formatDate .DownloadDate "2006" }}`
	case 'M':
		return `{{ formatDate .DownloadDate "01" }}`
	case 'D':
		return `{{ formatDate .DownloadDate "02" }}`
	default:
		return ""
	}
}

func DefaultOptions(cfg *config.Config) Options {
	if cfg == nil {
		cfg = config.Get()
	}

	return Options{
		Dir:                   cfg.DownloadDir,
		Template:              fileNameConfigTemplate(config.EffectiveFilename(cfg)),
		FilenameMaxLength:     config.EffectiveFilenameMax(cfg),
		PoolSize:              config.EffectivePoolSize(cfg),
		Threads:               config.EffectiveThreads(cfg),
		Limit:                 config.EffectiveLimit(cfg),
		Download:              cfg.Modules.Watch,
		TriggerReactions:      append([]string(nil), cfg.TriggerReactions...),
		Include:               append([]string(nil), cfg.Include...),
		Exclude:               append([]string(nil), cfg.Exclude...),
		FileSizeMB:            cfg.FileSizeMB,
		Forward:               cfg.Modules.Forward,
		ForwardMode:             config.EffectiveForwardMode(cfg),
		ForwardTarget:           cfg.Forward.Target,
		ForwardListen:           append([]string(nil), cfg.Forward.Listen...),
		ForwardListenComments:   cfg.Forward.ListenComments,
		ForwardSilent:           cfg.Forward.Silent,
		ForwardDedupeTTL:        time.Duration(config.EffectiveForwardDedupeTTL(cfg)) * time.Second,
		ForwardTriggerReactions: append([]string(nil), cfg.Forward.TriggerReactions...),
	}
}

func effectiveWatchOptionThreads(value int, cfg *config.Config) int {
	if value < 1 {
		return config.EffectiveThreads(cfg)
	}
	return value
}

func effectiveWatchOptionLimit(value int, cfg *config.Config) int {
	if value < 1 {
		return config.EffectiveLimit(cfg)
	}
	return value
}

func effectiveWatchOptionPoolSize(value int, cfg *config.Config) int {
	if value < 0 {
		return config.EffectivePoolSize(cfg)
	}
	return value
}

type fileTemplate struct {
	DialogID         int64
	MessageID        int
	TriggerMessageID int
	MessageDate      int64
	FileName         string
	FileCaption      string
	MessageTitle     string
	PeerName         string
	AlbumID          string
	F                string
	I                string
	G                string
	P                string
	S                string
	R                string
	A                string
	FileSize         string
	DownloadDate     int64
}

type downloadJob struct {
	peer   tg.InputPeerClass
	msgID  int
	peerID int64
	link   string
	source string
}

const (
	downloadJobSourceReaction    = "reaction"
	downloadJobSourceMessageLink = "message_link"
)

type aria2ConcurrentDownloadSetter interface {
	SetMaxConcurrentDownloads(ctx context.Context, limit int) error
}

type fileTask struct {
	msg        *tg.Message
	triggerMsg *tg.Message
	media      *tmedia.Media
	peer       tg.InputPeerClass
	peerID     int64
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

	dedup            sync.Map
	jobCh            chan downloadJob
	messageLinks     <-chan messageLinkSubmission
	triggerReactions map[string]struct{}
	include          map[string]struct{}
	exclude          map[string]struct{}
	minFileSizeBytes int64
	forward          *forwardRuntime
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.Get()
	if opts.Download {
		if err := validateWatchConfig(cfg); err != nil {
			return err
		}
	}
	if !opts.Download && !opts.Forward {
		return errors.New("watch has no enabled work: enable modules.watch or modules.forward")
	}
	if opts.Forward && strings.TrimSpace(opts.ForwardTarget) == "" {
		color.Yellow("⚠️ forward.target is empty; watched forwards will be sent to Saved Messages")
	}
	if opts.Forward && len(opts.ForwardListen) == 0 {
		color.Yellow("⚠️ modules.forward is enabled but forward.listen is empty")
	}
	if err := httpdl.ValidateBufferConfig(cfg.HTTP.Buffer); err != nil {
		return err
	}
	if opts.FileSizeMB < 0 {
		return errors.New("file_size_mb must be greater than or equal to 0")
	}
	opts.Threads = effectiveWatchOptionThreads(opts.Threads, cfg)
	opts.Limit = effectiveWatchOptionLimit(opts.Limit, cfg)
	opts.PoolSize = effectiveWatchOptionPoolSize(opts.PoolSize, cfg)
	downloaderMode := config.EffectiveDownloaderMode(cfg)

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

	parentCtx := ctx
	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(parentCtx))
	defer cancelRun()

	signalCtx, stopSignalNotify := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignalNotify()

	runtime := newWatchRuntime(cfg, opts, kvd, logctx.From(runCtx))
	var pauseOnShutdownOnce sync.Once
	pauseOnShutdown := func() {
		pauseOnShutdownOnce.Do(func() {
			color.Yellow("⏹ Stopping watcher...")
			switch downloaderMode {
			case config.DownloaderModeAria2:
				paused, err := watcharia2.PauseTDLTasksForShutdown(runCtx, runtime.aria2, runtime.aria2Tasks, cfg.HTTP.PublicBaseURL, logctx.From(runCtx))
				if err != nil {
					color.Yellow("⚠️ Failed to pause tdl aria2 tasks before shutdown: %v", err)
					return
				}
				if len(paused) > 0 {
					color.Yellow("⏸ Paused %d tdl aria2 task(s) before shutdown", len(paused))
				}
			case config.DownloaderModeInternal:
				paused, err := runtime.internal.PauseForShutdown(runCtx)
				if err != nil {
					color.Yellow("⚠️ Failed to pause internal download tasks before shutdown: %v", err)
					return
				}
				if len(paused) > 0 {
					color.Yellow("⏸ Paused %d internal download task(s) before shutdown", len(paused))
				}
			}
		})
	}

	go func() {
		select {
		case <-runCtx.Done():
			return
		case <-parentCtx.Done():
		case <-signalCtx.Done():
		}

		pauseOnShutdown()
		cancelRun()
	}()

	if opts.Download {
		switch downloaderMode {
		case config.DownloaderModeAria2:
			if err := waitForAria2(runCtx, runtime.aria2, opts.Limit, watcharia2.DefaultConnectRetryInterval, logctx.From(runCtx)); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return errors.Wrap(err, "configure aria2 max concurrent downloads")
			}
			logctx.From(runCtx).Info("Configured aria2 max concurrent downloads",
				zap.Int("limit", opts.Limit))
			runtime.telegramErrRegulator = watcharia2.NewTelegramErrorRegulator(runtime.aria2, runtime.aria2Tasks, cfg.HTTP.PublicBaseURL, logctx.From(runCtx))
			runtime.proxy.SetTelegramFileErrorReporter(runtime.telegramErrRegulator)
			go runtime.telegramErrRegulator.Run(runCtx)

			outputRoot, ensureOutputDirs, err := prepareAria2OutputRoot(runCtx, runtime.aria2, cfg)
			if err != nil {
				if opts.Notify != nil {
					opts.Notify(runCtx, fmt.Sprintf("aria2 下载目录异常：%v", err))
				}
				return errors.Wrap(err, "prepare aria2 output root")
			}
			runtime.outputRoot = outputRoot
			runtime.ensureOutputDirs = ensureOutputDirs
		case config.DownloaderModeInternal:
			outputRoot, fallback, err := prepareInternalOutputRoot(cfg)
			if err != nil {
				if opts.Notify != nil {
					opts.Notify(runCtx, fmt.Sprintf("内部下载目录异常：%v", err))
				}
				return errors.Wrap(err, "prepare internal output root")
			}
			if fallback {
				color.Yellow("⚠️ aria2.dir 不可用，内部下载器将使用备用目录：%s", outputRoot)
			}
			runtime.outputRoot = outputRoot
			runtime.ensureOutputDirs = true
		}
	}

	proxyErrCh := make(chan error, 1)
	httpListen := config.HTTPListenAddr(cfg)
	if opts.Download && cfg.Modules.HTTP && strings.TrimSpace(httpListen) != "" {
		go func() {
			if err := runtime.proxy.Start(runCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case proxyErrCh <- err:
				default:
				}
				cancelRun()
			}
		}()
	}

	if opts.Download && opts.Forward {
		color.Green("👀 Watching for reactions and forward sources... Press Ctrl+C to stop")
	} else if opts.Forward {
		color.Green("👀 Watching forward sources... Press Ctrl+C to stop")
	} else {
		color.Green("👀 Watching for reactions... Press Ctrl+C to stop")
	}
	if opts.Download && cfg.Modules.HTTP && strings.TrimSpace(httpListen) != "" {
		color.Green("   HTTP listen: %s", httpListen)
	}
	if opts.Download && downloaderMode == config.DownloaderModeAria2 {
		color.Green("   Public base URL: %s", cfg.HTTP.PublicBaseURL)
		color.Green("   aria2 RPC: %s", cfg.Aria2.RPCURL)
	}
	if opts.Download {
		color.Green("   Downloader mode: %s", downloaderMode)
		color.Green("   Output root: %s", runtime.outputRoot)
		color.Green("   Download dir template: %s", opts.Dir)
	}
	poolSizeLabel := fmt.Sprintf("%d", opts.PoolSize)
	if opts.PoolSize == 0 {
		poolSizeLabel = "unlimited"
	}
	color.Green("   Telegram DC pool size: %s", poolSizeLabel)
	if opts.Download {
		color.Green("   Per-file threads: %d", opts.Threads)
		color.Green("   Max concurrent downloads: %d", opts.Limit)
		if cfg.HTTP.DownloadLinkTTLHours <= 0 {
			color.Green("   Download link TTL: permanent")
		} else {
			color.Green("   Download link TTL: %dh", cfg.HTTP.DownloadLinkTTLHours)
		}
		if httpdl.NormalizeBufferMode(cfg.HTTP.Buffer.Mode) == httpdl.BufferModeMemory {
			color.Green("   HTTP buffer: memory (%d MiB shared, 5s retention)", httpdl.NormalizedBufferSizeMB(cfg.HTTP.Buffer))
		} else {
			color.Green("   HTTP buffer: off")
		}
		color.Green("   HTTP transfer mode: %s", config.EffectiveHTTPTransferMode(cfg))
		if downloaderMode == config.DownloaderModeAria2 {
			color.Green("   HTTP range connections: %d", config.EffectiveHTTPRangeConnections(cfg))
		}
		color.Green("   Trigger reactions: %s", formatTriggerReactions(opts.TriggerReactions))
		if opts.FileSizeMB > 0 {
			color.Green("   Min file size: %s (%d MB)", utils.Byte.FormatBinaryBytes(fileSizeMBToBytes(opts.FileSizeMB)), opts.FileSizeMB)
		} else {
			color.Green("   Min file size: unlimited")
		}
	}
	if opts.Forward {
		color.Green("   Forward mode: %s", opts.ForwardMode)
		color.Green("   Forward target: %s", forwardTargetLabel(opts.ForwardTarget))
		color.Green("   Forward listen: %s", formatForwardListen(opts.ForwardListen))
		color.Green("   Forward comments: %t", opts.ForwardListenComments)
		color.Green("   Forward trigger reactions: %s", formatTriggerReactions(opts.ForwardTriggerReactions))
	}
	if opts.Download && downloaderMode == config.DownloaderModeAria2 {
		warnPublicBaseURL(cfg.HTTP.PublicBaseURL)
	}

	reconnectDelay := time.Duration(cfg.ReconnectTimeout) * time.Second
	if reconnectDelay <= 0 {
		reconnectDelay = 5 * time.Second
	}
	var pausedAria2GIDs []string
	takeProxyErr := func() error {
		select {
		case err := <-proxyErrCh:
			return err
		default:
			return nil
		}
	}

	for {
		select {
		case err := <-proxyErrCh:
			return errors.Wrap(err, "start http proxy")
		default:
		}

		if runCtx.Err() != nil {
			if err := takeProxyErr(); err != nil {
				return errors.Wrap(err, "start http proxy")
			}
			return nil
		}

		resumed, err := runOnce(runCtx, opts, tpl, kvd, reconnectDelay, runtime, pausedAria2GIDs)
		if resumed {
			pausedAria2GIDs = nil
		}
		if err == nil || errors.Is(err, context.Canceled) {
			if proxyErr := takeProxyErr(); proxyErr != nil {
				return errors.Wrap(proxyErr, "start http proxy")
			}
			return nil
		}

		color.Yellow("⚠️ Watcher disconnected: %v", err)
		if opts.Download && downloaderMode == config.DownloaderModeAria2 {
			newPausedGIDs, pauseErr := watcharia2.SuspendTDLTasksForReconnect(runCtx, runtime.aria2, runtime.aria2Tasks, cfg.HTTP.PublicBaseURL, logctx.From(runCtx))
			if pauseErr != nil {
				if errors.Is(pauseErr, context.Canceled) {
					if proxyErr := takeProxyErr(); proxyErr != nil {
						return errors.Wrap(proxyErr, "start http proxy")
					}
					return nil
				}
				return errors.Wrap(pauseErr, "pause aria2 tasks before reconnect")
			}
			pausedAria2GIDs = watcharia2.MergeUniqueGIDs(pausedAria2GIDs, newPausedGIDs)
			if len(newPausedGIDs) > 0 {
				color.Yellow("⏸ Paused %d tdl aria2 task(s) before reconnect", len(newPausedGIDs))
			}
		}
		color.Yellow("🔄 Reconnecting in %v...", reconnectDelay)

		select {
		case err := <-proxyErrCh:
			return errors.Wrap(err, "start http proxy")
		case <-runCtx.Done():
			if proxyErr := takeProxyErr(); proxyErr != nil {
				return errors.Wrap(proxyErr, "start http proxy")
			}
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func runOnce(ctx context.Context, opts Options, tpl *template.Template, kvd storage.Storage, reconnectDelay time.Duration, runtime *watchRuntime, pausedAria2GIDs []string) (resumed bool, rerr error) {
	cfg := config.Get()
	poolSize := effectiveWatchOptionPoolSize(opts.PoolSize, cfg)
	downloaderMode := config.EffectiveDownloaderMode(cfg)

	o := pkgtclient.Options{
		KV:               kvd,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: reconnectDelay,
	}

	d := tg.NewUpdateDispatcher()
	w := &Watcher{
		opts:             opts,
		tpl:              tpl,
		runtime:          runtime,
		jobCh:            make(chan downloadJob, 100),
		messageLinks:     opts.messageLinks,
		triggerReactions: newTriggerReactionSet(opts.TriggerReactions),
		include:          filterMap.New(opts.Include, addPrefixDot),
		exclude:          filterMap.New(opts.Exclude, addPrefixDot),
		minFileSizeBytes: fileSizeMBToBytes(opts.FileSizeMB),
	}

	if opts.Download || (opts.Forward && len(opts.ForwardTriggerReactions) > 0) {
		d.OnMessageReactions(w.onReaction)
		d.OnEditMessage(w.onEditMessage)
		d.OnEditChannelMessage(w.onEditChannelMessage)
	}
	if opts.Forward {
		d.OnNewMessage(w.onNewMessageForward)
		d.OnNewChannelMessage(w.onNewChannelMessageForward)
	}
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
			int64(poolSize),
			tclient.NewDefaultMiddlewares(ctx, reconnectDelay)...)
		defer multierr.AppendInvoke(&rerr, multierr.Close(pool))
		defer runtime.pools.Set(nil)

		runtime.pools.Set(pool)

		w.pool = pool
		w.manager = peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))
		w.configureForward(ctx)

		self, err := client.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self user")
		}
		if downloaderMode == config.DownloaderModeInternal && runtime.internal != nil {
			if err := runtime.internal.Start(ctx); err != nil {
				return errors.Wrap(err, "start internal downloader")
			}
			defer runtime.internal.Stop()
		}
		if downloaderMode == config.DownloaderModeAria2 && len(pausedAria2GIDs) > 0 {
			if err := watcharia2.ResumeTDLTasks(ctx, runtime.aria2, pausedAria2GIDs, logctx.From(ctx)); err != nil {
				return errors.Wrap(err, "resume paused aria2 tasks")
			}
			resumed = true
			color.Green("▶ Resumed %d tdl aria2 task(s) after reconnect", len(watcharia2.UniqueGIDs(pausedAria2GIDs)))
		}
		updatesDone := make(chan struct{})
		go func() {
			defer close(updatesDone)
			if err := updatesMgr.Run(ctx, pool.Default(ctx), self.ID, updates.AuthOptions{
				IsBot: false,
			}); err != nil && !errors.Is(err, context.Canceled) {
				logctx.From(ctx).Error("Updates manager stopped with error", zap.Error(err))
			}
		}()

		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(effectiveWatchOptionLimit(opts.Limit, cfg))
		if opts.Download {
			go w.dispatcher(egCtx, eg)
		}

		<-ctx.Done()

		if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			logctx.From(ctx).Error("Submission goroutine error", zap.Error(err))
		}
		select {
		case <-updatesDone:
		case <-time.After(5 * time.Second):
			logctx.From(ctx).Warn("Updates manager did not stop before timeout")
		}

		return nil
	})
	return resumed, err
}

func waitForAria2(ctx context.Context, client aria2ConcurrentDownloadSetter, limit int, retryInterval time.Duration, logger *zap.Logger) error {
	if retryInterval <= 0 {
		retryInterval = watcharia2.DefaultConnectRetryInterval
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	for {
		err := client.SetMaxConcurrentDownloads(ctx, limit)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || !watcharia2.IsConnectionError(err) {
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
	if _, err := config.NormalizeHTTPTransferMode(cfg.HTTP.TransferMode); err != nil {
		return err
	}
	if err := httpdl.ValidateBufferConfig(cfg.HTTP.Buffer); err != nil {
		return err
	}
	switch config.EffectiveDownloaderMode(cfg) {
	case config.DownloaderModeAria2:
		if !cfg.Modules.HTTP {
			return errors.New("modules.http must be enabled when downloader.mode is aria2")
		}
		if strings.TrimSpace(config.HTTPListenAddr(cfg)) == "" {
			return errors.New("http.address or http.port is empty")
		}
		if cfg.HTTP.PublicBaseURL == "" {
			return errors.New("http.public_base_url is empty, please set it in config.json")
		}
		if cfg.Aria2.RPCURL == "" {
			return errors.New("aria2.rpc_url is empty, please set it in config.json")
		}
	case config.DownloaderModeInternal:
	default:
		return fmt.Errorf("unsupported downloader mode %q", cfg.Downloader.Mode)
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

	if w.forward != nil && w.forward.enabled && len(w.forward.triggerReactions) > 0 {
		if _, ok := w.forward.listen[peerID]; ok {
			if w.hasMyForwardReactionTrigger(&update.Reactions) {
				go w.triggerForwardOnReaction(ctx, e, update.Peer, peerID, update.MsgID)
			}
		}
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

	if w.forward != nil && w.forward.enabled && len(w.forward.triggerReactions) > 0 {
		if _, ok := w.forward.listen[peerID]; ok {
			if w.hasMyForwardReactionTrigger(&msg.Reactions) {
				go w.triggerForwardOnReaction(ctx, e, msg.PeerID, peerID, msg.ID)
			}
		}
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

func (w *Watcher) hasMyForwardReactionTrigger(reactions *tg.MessageReactions) bool {
	if w.forward == nil || reactions == nil || reactions.Min {
		return false
	}
	triggers := w.forward.triggerReactions
	if recent, ok := reactions.GetRecentReactions(); ok {
		for _, r := range recent {
			if r.My {
				if _, ok := triggers[normalizeTriggerReaction(reactionEmoji(r.Reaction))]; ok {
					return true
				}
			}
		}
	}
	for _, rc := range reactions.Results {
		if _, ok := rc.GetChosenOrder(); ok {
			if _, ok := triggers[normalizeTriggerReaction(reactionEmoji(rc.Reaction))]; ok {
				return true
			}
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

func formatTriggerReactions(values []string) string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeTriggerReaction(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return "any"
	}
	return strings.Join(normalized, ", ")
}

func formatForwardListen(values []string) string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return "(empty)"
	}
	return strings.Join(normalized, ", ")
}

func forwardTargetLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Saved Messages"
	}
	return value
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

func (w *Watcher) dispatcher(ctx context.Context, eg *errgroup.Group) {
	for {
		select {
		case <-ctx.Done():
			return
		case submission := <-w.messageLinks:
			result, err := w.submitMessageLink(ctx, eg, submission.link)
			submission.reply <- messageLinkSubmissionResponse{result: result, err: err}
		case job := <-w.jobCh:
			if ctx.Err() != nil {
				return
			}
			_, _ = w.processDownloadJob(ctx, eg, job)
		}
	}
}

func (w *Watcher) submitMessageLink(ctx context.Context, eg *errgroup.Group, link string) (MessageLinkSubmissionResult, error) {
	link, err := ValidateTelegramMessageHTTPLink(link)
	if err != nil {
		return MessageLinkSubmissionResult{Link: link}, err
	}

	peer, msgID, err := tutil.ParseMessageLink(ctx, w.manager, link)
	if err != nil {
		return MessageLinkSubmissionResult{Link: link}, errors.Wrap(err, "解析 Telegram 消息链接")
	}

	job := downloadJob{
		peer:   peer.InputPeer(),
		msgID:  msgID,
		peerID: peer.ID(),
		link:   link,
		source: downloadJobSourceMessageLink,
	}
	return w.processDownloadJob(ctx, eg, job)
}

func (w *Watcher) processDownloadJob(ctx context.Context, eg *errgroup.Group, job downloadJob) (MessageLinkSubmissionResult, error) {
	result := MessageLinkSubmissionResult{
		Link:      job.link,
		PeerID:    job.peerID,
		MessageID: job.msgID,
	}
	logctx.From(ctx).Info("Dispatcher processing job",
		zap.Int64("peer_id", job.peerID),
		zap.Int("msg_id", job.msgID),
		zap.Bool("peer_nil", job.peer == nil),
		zap.String("source", job.source))

	peer := job.peer
	if peer == nil {
		resolved, err := w.resolvePeer(ctx, job.peerID)
		if err != nil {
			logctx.From(ctx).Error("Failed to resolve peer",
				zap.Int64("peer_id", job.peerID),
				zap.Error(err))
			color.Red("❌ Cannot resolve peer %d: %v", job.peerID, err)
			w.notify(ctx, "无法解析消息来源，下载任务未提交。\n消息：%s\nPeer: %d\n错误：%v", job.link, job.peerID, err)
			return result, err
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
		return result, err
	}

	collection, err := w.collectFiles(ctx, msg, peer, job.peerID)
	if err != nil {
		color.Red("❌ Failed to collect files for msg %d: %v", job.msgID, err)
		w.notify(ctx, "解析消息媒体失败，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, job.msgID, err)
		return result, err
	}
	result.Total = collection.total

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
	result.Queued = len(prepared)
	result.Skipped = collection.skipped

	w.notify(ctx, "%s\n链接：%s\n文件总数：%d\n需要下载：%d\n跳过：%d", downloadJobNotice(job), job.link, collection.total, len(prepared), collection.skipped)
	if len(prepared) == 0 {
		return result, nil
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
					target := "aria2"
					if config.EffectiveDownloaderMode(config.Get()) == config.DownloaderModeInternal {
						target = "内部下载队列"
					}
					w.notify(ctx, "提交到%s失败。\n文件：%s\n消息 ID: %d\n错误：%v", target, f.file.media.Name, f.file.msg.ID, err)
				}
			}
			return nil
		})
	}

	return result, nil
}

func downloadJobNotice(job downloadJob) string {
	if job.source == downloadJobSourceMessageLink {
		return "收到 Telegram 消息链接，已进入下载流程。"
	}
	return "监听到了新增回应触发。"
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
			if !w.matchFilter(media.Name, media.Size) {
				color.Yellow("⏭ Skipping filtered (album): %s", media.Name)
				collection.skipped++
				continue
			}
			collection.files = append(collection.files, fileTask{msg: m, triggerMsg: msg, media: media, peer: peer, peerID: peerID})
		}

		return collection, nil
	}

	media, ok := tmedia.GetMedia(msg)
	if !ok {
		color.Yellow("⚠️ Message %d has no media, skipping", msg.ID)
		return collection, nil
	}
	collection.total++
	if !w.matchFilter(media.Name, media.Size) {
		color.Yellow("⏭ Skipping filtered: %s", media.Name)
		collection.skipped++
		return collection, nil
	}

	collection.files = append(collection.files, fileTask{msg: msg, triggerMsg: msg, media: media, peer: peer, peerID: peerID})
	return collection, nil
}

func (w *Watcher) matchFilter(name string, size int64) bool {
	if !w.matchExtensionFilter(name) {
		return false
	}
	return w.matchFileSizeFilter(size)
}

func (w *Watcher) matchExtensionFilter(name string) bool {
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

func (w *Watcher) matchFileSizeFilter(size int64) bool {
	return w.minFileSizeBytes <= 0 || size >= w.minFileSizeBytes
}

func fileSizeMBToBytes(mb int64) int64 {
	if mb <= 0 {
		return 0
	}
	const maxInt64 = int64(1<<63 - 1)
	if mb > maxInt64/bytesPerMegabyte {
		return maxInt64
	}
	return mb * bytesPerMegabyte
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
	data := w.downloadDirData(ctx, file)
	fileName, err := w.renderFileName(dialogID, data.Name, data.Time, file.msg, file.triggerMsg, file.media)
	if err != nil {
		return preparedFileTask{}, false, err
	}

	baseDir := joinTargetPath(w.runtime.outputRoot, renderDownloadDir(w.opts.Dir, data)...)
	dir, out, fullPath := resolveTargetPath(baseDir, fileName)
	if w.runtime.ensureOutputDirs && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return preparedFileTask{}, false, errors.Wrap(err, "create target directory")
		}
	}
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
	cfg := config.Get()
	file := prepared.file
	task, err := w.runtime.proxy.NewTask(ctx, file.peerID, file.msg.ID, file.peer, prepared.fileName, file.media.Size, file.media)
	if err != nil {
		return errors.Wrap(err, "register download task")
	}

	if config.EffectiveDownloaderMode(cfg) == config.DownloaderModeInternal {
		if _, err := w.runtime.internal.Add(ctx, task, prepared); err != nil {
			return errors.Wrap(err, "queue internal download")
		}
		logctx.From(ctx).Info("Queued internal download task",
			zap.Int64("peer_id", file.peerID),
			zap.Int("msg_id", file.msg.ID),
			zap.String("file_name", prepared.fileName),
			zap.String("target_path", prepared.fullPath),
			zap.String("task_id", task.ID))

		color.Green("🚀 Queued internal download: msg %d -> %s", file.msg.ID, prepared.fullPath)
		color.Green("   Task: %s", task.ID)
		return nil
	}

	downloadURL, err := w.runtime.proxy.BuildURL(task.ID)
	if err != nil {
		return errors.Wrap(err, "build download url")
	}

	connections := config.HTTPRangeConnectionsFor(cfg.HTTP, w.opts.Threads)
	gid, err := w.runtime.aria2.AddURI(ctx, downloadURL, watcharia2.AddURIOptions{
		Dir:         prepared.dir,
		Out:         prepared.out,
		Connections: connections,
	})
	if err != nil {
		return errors.Wrap(err, "submit to aria2")
	}
	if err := w.runtime.aria2Tasks.Add(ctx, watcharia2.TaskRecord{
		GID:          gid,
		TaskID:       task.ID,
		DownloadURL:  downloadURL,
		Dir:          prepared.dir,
		Out:          prepared.out,
		Connections:  connections,
		TransferMode: config.EffectiveHTTPTransferMode(cfg),
		CreatedAt:    time.Now(),
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

func (w *Watcher) renderFileName(dialogID int64, peerName string, downloadedAt time.Time, msg, triggerMsg *tg.Message, media *tmedia.Media) (string, error) {
	if triggerMsg == nil {
		triggerMsg = msg
	}
	if downloadedAt.IsZero() {
		downloadedAt = time.Now()
	}
	messageTitle := ""
	triggerMessageID := 0
	if triggerMsg != nil {
		messageTitle = strings.TrimSpace(triggerMsg.Message)
		triggerMessageID = triggerMsg.ID
	}
	albumID := ""
	if groupedID, ok := msg.GetGroupedID(); ok {
		albumID = fmt.Sprint(groupedID)
	}

	ext := filepath.Ext(media.Name)
	stem := strings.TrimSuffix(media.Name, ext)
	fValue := safePathSegment(stem)
	hasF := strings.Contains(w.opts.Template, "{{ .F }}") || strings.Contains(w.opts.Template, "filenamify .FileName")
	hasI := strings.Contains(w.opts.Template, "{{ .I }}")

	appendExt := func(s string) string {
		// collapse consecutive dashes/underscores that may result from empty template vars (e.g., dedup of I)
		prefix, leaf := splitRenderedNameLeaf(s)
		for strings.Contains(leaf, "--") {
			leaf = strings.ReplaceAll(leaf, "--", "-")
		}
		for strings.Contains(leaf, "__") {
			leaf = strings.ReplaceAll(leaf, "__", "_")
		}
		s = prefix + leaf
		if ext != "" && !strings.HasSuffix(s, ext) {
			return s + ext
		}
		return s
	}

	render := func(messageTitleMax int) (string, error) {
		iValue := safeMessageTitleSegmentWithMax(messageTitle, messageTitleMax)
		if hasF && hasI && iValue != "" && strings.Contains(fValue, iValue) {
			iValue = ""
		}
		var toName bytes.Buffer
		if err := w.tpl.Execute(&toName, &fileTemplate{
			DialogID:         dialogID,
			MessageID:        msg.ID,
			TriggerMessageID: triggerMessageID,
			MessageDate:      int64(msg.Date),
			FileName:         stem,
			FileCaption:      msg.Message,
			MessageTitle:     messageTitle,
			PeerName:         peerName,
			AlbumID:          albumID,
			F:                fValue,
			I:                iValue,
			G:                safePathSegment(peerName),
			P:                fmt.Sprint(dialogID),
			S:                fmt.Sprint(msg.ID),
			R:                fmt.Sprint(triggerMessageID),
			A:                safePathSegment(albumID),
			FileSize:         utils.Byte.FormatBinaryBytes(media.Size),
			DownloadDate:     downloadedAt.Unix(),
		}); err != nil {
			return "", errors.Wrap(err, "execute template")
		}
		return appendExt(toName.String()), nil
	}

	rendered, err := render(safeMessageTitleMaxRunes)
	if err != nil {
		return "", err
	}
	maxLength := w.opts.FilenameMaxLength
	if maxLength <= 0 || renderedNameLeafRuneLen(rendered) <= maxLength {
		return rendered, nil
	}

	if strings.Contains(w.opts.Template, ".I") {
		best := ""
		found := false
		for low, high := 0, safeMessageTitleMaxRunes; low <= high; {
			mid := (low + high) / 2
			candidate, err := render(mid)
			if err != nil {
				return "", err
			}
			if renderedNameLeafRuneLen(candidate) <= maxLength {
				best = candidate
				found = true
				low = mid + 1
			} else {
				high = mid - 1
			}
		}
		if found {
			return best, nil
		}
	}

	return limitRenderedNameLeaf(rendered, maxLength), nil
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
