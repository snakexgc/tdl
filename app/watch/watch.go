package watch

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/log/logzap"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	appforward "github.com/snakexgc/tdl/app/forward"
	"github.com/snakexgc/tdl/core/dcpool"
	"github.com/snakexgc/tdl/core/logctx"
	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/core/tclient"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/filterMap"
	"github.com/snakexgc/tdl/pkg/kv"
	pkgtclient "github.com/snakexgc/tdl/pkg/tclient"
	"github.com/snakexgc/tdl/pkg/tplfunc"
	"github.com/snakexgc/tdl/pkg/utils"
)

const bytesPerMegabyte int64 = 1024 * 1024

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
	if opts.FileSizeMB < 0 {
		return errors.New("file_size_mb must be greater than or equal to 0")
	}
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
			if downloaderMode == config.DownloaderModeInternal {
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
			// The target path is metadata for an optional external submitter. Never
			// query that backend while starting the Telegram watcher.
			runtime.outputRoot = cleanTargetRoot(cfg.Aria2.Dir)
			if runtime.outputRoot == "" {
				runtime.outputRoot = "."
			}
			runtime.ensureOutputDirs = false
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

	if opts.Download && opts.Forward {
		color.Green("👀 Watching for reactions and forward sources... Press Ctrl+C to stop")
	} else if opts.Forward {
		color.Green("👀 Watching forward sources... Press Ctrl+C to stop")
	} else {
		color.Green("👀 Watching for reactions... Press Ctrl+C to stop")
	}
	if opts.Download && downloaderMode == config.DownloaderModeAria2 {
		color.Green("   Public base URL: %s", cfg.HTTP.PublicBaseURL)
		if opts.DownloadSubmitter != nil {
			color.Green("   Download submitter: %s", opts.DownloadSubmitter.Name())
		} else {
			color.Green("   Download submitter: none (links only)")
		}
	}
	if opts.Download {
		color.Green("   Downloader mode: %s", downloaderMode)
		color.Green("   Output root: %s", runtime.outputRoot)
		color.Green("   Download dir template: %s", opts.Dir)
	}
	color.Green("   Telegram DC pool size: %d", opts.PoolSize)
	if opts.Download {
		color.Green("   Per-DC connection and download capacity: %d", opts.PoolSize)
		color.Green("   Max concurrent downloads: %d", opts.Limit)
		if cfg.HTTP.DownloadLinkTTLHours <= 0 {
			color.Green("   Download link TTL: permanent")
		} else {
			color.Green("   Download link TTL: %dh", cfg.HTTP.DownloadLinkTTLHours)
		}
		if downloaderMode == config.DownloaderModeAria2 {
			color.Green("   HTTP Range connections per aria2 task: %d", opts.PoolSize)
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
	for {
		if runCtx.Err() != nil {
			return nil
		}

		err := runOnce(runCtx, opts, tpl, kvd, reconnectDelay, runtime)
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}

		color.Yellow("⚠️ Watcher disconnected: %v", err)
		color.Yellow("🔄 Reconnecting in %v...", reconnectDelay)

		select {
		case <-runCtx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func runOnce(ctx context.Context, opts Options, tpl *template.Template, kvd storage.Storage, reconnectDelay time.Duration, runtime *watchRuntime) (rerr error) {
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

	// Register reaction handlers whenever download or forward is enabled. Forward
	// reacts on its trigger emoji (or any emoji when its trigger set is empty),
	// so it needs these handlers regardless of how many triggers are configured.
	if opts.Download || opts.Forward {
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
		Logger: logzap.New(logctx.From(ctx).Named("updates")),
	})
	o.UpdateHandler = updatesMgr

	client, err := pkgtclient.New(ctx, o, false)
	if err != nil {
		return errors.Wrap(err, "create client")
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

		// Drain the persistent forward queue one job at a time using this
		// connection's pool. Runs whenever the watcher is connected so /forward
		// jobs are processed even if the auto-forward listener is disabled.
		forwardDone := make(chan struct{})
		go func() {
			defer close(forwardDone)
			if err := appforward.Jobs().Serve(egCtx, appforward.Runtime{
				Pool:     pool,
				Manager:  w.manager,
				PoolSize: opts.PoolSize,
			}); err != nil && !errors.Is(err, context.Canceled) {
				logctx.From(ctx).Error("Forward queue worker stopped", zap.Error(err))
			}
		}()

		<-ctx.Done()

		if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			logctx.From(ctx).Error("Submission goroutine error", zap.Error(err))
		}
		select {
		case <-updatesDone:
		case <-time.After(5 * time.Second):
			logctx.From(ctx).Warn("Updates manager did not stop before timeout")
		}
		select {
		case <-forwardDone:
		case <-time.After(5 * time.Second):
			logctx.From(ctx).Warn("Forward queue worker did not stop before timeout")
		}

		return nil
	})
	return err
}

func validateWatchConfig(cfg *config.Config) error {
	switch config.EffectiveDownloaderMode(cfg) {
	case config.DownloaderModeAria2:
		if cfg.HTTP.PublicBaseURL == "" {
			return errors.New("http.public_base_url is empty, please set it in config.json")
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
		color.Yellow("⚠️ http.public_base_url uses %s; external downloaders usually cannot use this address directly", u.Hostname())
	case "localhost":
		color.Yellow("⚠️ http.public_base_url uses localhost; this only works when the downloader shares this machine and network namespace")
	default:
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			color.Yellow("⚠️ http.public_base_url uses loopback address %s; this only works when the downloader shares this machine and network namespace", u.Hostname())
		}
	}
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
