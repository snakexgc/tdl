package watch

import (
	"context"
	"fmt"
	"net"
	"net/http"
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

	appforward "github.com/iyear/tdl/app/forward"
	httpdl "github.com/iyear/tdl/app/http"
	watcharia2 "github.com/iyear/tdl/app/watch/aria2"
	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tclient"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/filterMap"
	"github.com/iyear/tdl/pkg/kv"
	pkgtclient "github.com/iyear/tdl/pkg/tclient"
	"github.com/iyear/tdl/pkg/tplfunc"
	"github.com/iyear/tdl/pkg/utils"
)

const bytesPerMegabyte int64 = 1024 * 1024

type aria2ConcurrentDownloadSetter interface {
	SetMaxConcurrentDownloads(ctx context.Context, limit int) error
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

			runtime.zeroSpeedMonitor = watcharia2.NewZeroSpeedMonitor(runtime.aria2, runtime.aria2Tasks, cfg.HTTP.PublicBaseURL, logctx.From(runCtx))
			go runtime.zeroSpeedMonitor.Run(runCtx)

			if count, err := watcharia2.ResumeStartupPausedTasks(runCtx, runtime.aria2, runtime.aria2Tasks, cfg.HTTP.PublicBaseURL, logctx.From(runCtx)); err != nil {
				if !errors.Is(err, context.Canceled) {
					logctx.From(runCtx).Warn("Failed to resume paused aria2 tasks at startup", zap.Error(err))
				}
			} else if count > 0 {
				color.Green("▶ Resumed %d paused tdl aria2 task(s) at startup", count)
			}

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

		// Drain the persistent forward queue one job at a time using this
		// connection's pool. Runs whenever the watcher is connected so /forward
		// jobs are processed even if the auto-forward listener is disabled.
		forwardDone := make(chan struct{})
		go func() {
			defer close(forwardDone)
			if err := appforward.Jobs().Serve(egCtx, appforward.Runtime{
				Pool:    pool,
				Manager: w.manager,
				Threads: opts.Threads,
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
