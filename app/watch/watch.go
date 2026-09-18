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
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/dcpool"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tclient"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	pkgtclient "github.com/snakexgc/tdl/pkg/tclient"
	"github.com/snakexgc/tdl/rte"
)

type Watcher struct {
	intentMu       sync.RWMutex
	intents        ports.DownloadIntents
	forwardIntents ports.ForwardIntents
	opts           Options
	pool           dcpool.Pool
	manager        *peers.Manager
	runtime        *watchRuntime

	triggerOnce      sync.Once
	trigger          ports.ReactionTrigger
	triggerErr       error
	triggerStop      func()
	jobCh            chan downloadJob
	messageLinks     <-chan messageLinkSubmission
	triggerReactions map[string]struct{}
	forward          *forwardRuntime
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.Get()
	account := types.AccountID(cfg.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	var route ports.DownloadRoute
	if opts.DownloadRouting != nil {
		var err error
		route, err = opts.DownloadRouting.Route(ctx, account)
		if err != nil {
			return err
		}
	}
	if opts.Download && len(route.Executors) == 0 {
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
	opts.FileSizeMinMB, opts.FileSizeMaxMB, _ = config.NormalizeFileSizeRange(opts.FileSizeMinMB, opts.FileSizeMaxMB)
	if opts.Filter == nil || opts.Naming == nil {
		policies, filter, naming, err := startPolicies(ctx, cfg.Namespace, opts)
		if err != nil {
			return errors.Wrap(err, "start policy components")
		}
		defer func() { _ = policies.Stop(context.Background()) }()
		opts.Filter = filter
		opts.Naming = naming
	}
	opts.Account = types.AccountID(cfg.Namespace)
	if opts.Account == "" {
		opts.Account = types.DefaultAccount
	}
	opts.Limit = effectiveWatchOptionLimit(opts.Limit, cfg)
	opts.PoolSize = effectiveWatchOptionPoolSize(opts.PoolSize, cfg)
	downloaderMode := config.EffectiveDownloaderMode(cfg)

	kvd, err := kv.From(ctx).Open(cfg.Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}

	if opts.ForwardQueue == nil {
		opts.ForwardQueue = appforward.NewQueue(kvd)
		opts.ForwardQueue.SetNotifier(opts.Notify)
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
			if opts.Download && runtime.internal != nil {
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

	if opts.Download && len(route.Executors) == 0 {
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
				color.Yellow("⚠️ aria2.dir 不可用，本地下载器将使用备用目录：%s", outputRoot)
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
		color.Green("   File size range: %d ~ %d MB (0 means unlimited)", opts.FileSizeMinMB, opts.FileSizeMaxMB)
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

		err := runOnce(runCtx, opts, kvd, reconnectDelay, runtime)
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

func runOnce(ctx context.Context, opts Options, kvd storage.Storage, reconnectDelay time.Duration, runtime *watchRuntime) (rerr error) {
	cfg := config.Get()
	poolSize := effectiveWatchOptionPoolSize(opts.PoolSize, cfg)

	o := pkgtclient.Options{
		Connections: opts.Connections,
		Credentials: opts.Credentials, Account: opts.Account,
		KV:               kvd,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: reconnectDelay,
	}

	d := tg.NewUpdateDispatcher()
	w := &Watcher{
		opts:             opts,
		runtime:          runtime,
		jobCh:            make(chan downloadJob, 100),
		messageLinks:     opts.messageLinks,
		triggerReactions: newTriggerReactionSet(opts.TriggerReactions),
	}

	if _, err := w.reactionPolicy(ctx); err != nil {
		return err
	}
	if w.triggerStop != nil {
		defer w.triggerStop()
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

	updateStore, err := tgauth.UpdateStore(kvd)
	if err != nil {
		return errors.Wrap(err, "open update-state dataset")
	}
	accessHashes, err := tgauth.NewAccessHashes(kvd)
	if err != nil {
		return errors.Wrap(err, "open access-hash dataset")
	}
	peerStore, err := tgauth.PeersStore(kvd)
	if err != nil {
		return errors.Wrap(err, "open peer dataset")
	}
	updatesMgr := updates.New(updates.Config{
		Storage:          updateStore,
		AccessHasher:     accessHashes,
		UserAccessHasher: accessHashes,
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
		pool := dcpool.NewPool(client.Client,
			int64(poolSize),
			tclient.NewDefaultMiddlewares(ctx, reconnectDelay)...)
		defer multierr.AppendInvoke(&rerr, multierr.Close(pool))
		defer runtime.pools.Set(nil)

		runtime.pools.Set(pool)

		w.pool = pool
		w.manager = peers.Options{Storage: peerStore}.Build(pool.Default(ctx))
		w.configureForward(ctx)

		self, err := client.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self user")
		}
		if opts.Download && runtime.internal != nil {
			host, executor, err := application.LocalDownloadHost(ctx, opts.Account, runtime.internal.component(), opts.ComponentStore)
			if err != nil {
				return errors.Wrap(err, "start local downloader component")
			}
			runtime.local = executor
			if opts.SetDownloadHost != nil {
				opts.SetDownloadHost(host)
				defer opts.SetDownloadHost(nil)
			}
			defer func() { _ = host.Stop(context.Background()) }()
		}
		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(effectiveWatchOptionLimit(opts.Limit, cfg))
		var intentHost *rte.Runtime
		if opts.Download || opts.Forward {
			var download ports.DownloadIntentHandler
			if opts.Download {
				download = func(intentCtx context.Context, request types.DownloadIntent) error {
					_, err := w.processDownloadJob(intentCtx, eg, downloadJob{peer: protocolPeer(request.Peer), peerID: request.PeerID, msgID: request.MessageID, link: request.Link, source: request.Source})
					return err
				}
			}
			var forward ports.ForwardIntentHandler
			if opts.Forward {
				forward = w.processForwardIntent
			}
			var port ports.DownloadIntents
			var forwardPort ports.ForwardIntents
			intentHost, port, forwardPort, err = application.IntentHost(ctx, opts.Account, download, forward, func(intentCtx context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
				return w.processDownloadJob(intentCtx, eg, downloadJob{peer: protocolPeer(request.Peer), peerID: request.PeerID, msgID: request.MessageID, link: request.Link, source: request.Source})
			})
			if err != nil {
				return err
			}
			w.intentMu.Lock()
			w.intents = port
			w.forwardIntents = forwardPort
			w.intentMu.Unlock()
			defer func() { _ = intentHost.Stop(context.Background()) }()
			if opts.SetIntentHost != nil {
				opts.SetIntentHost(intentHost)
				defer opts.SetIntentHost(nil)
			}
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

		dispatchDone := make(chan struct{})
		if opts.Download {
			go func() {
				defer close(dispatchDone)
				w.dispatcher(egCtx, eg)
			}()
		} else {
			close(dispatchDone)
		}

		// Drain the persistent forward queue one job at a time using this
		// connection's pool. Runs whenever the watcher is connected so /forward
		// jobs are processed even if the auto-forward listener is disabled.
		forwardDone := make(chan struct{})
		go func() {
			defer close(forwardDone)
			if err := opts.ForwardQueue.Serve(egCtx, appforward.Runtime{
				Pool:           pool,
				Manager:        w.manager,
				PoolSize:       opts.PoolSize,
				Account:        opts.Account,
				ComponentStore: opts.ComponentStore,
			}); err != nil && !errors.Is(err, context.Canceled) {
				logctx.From(ctx).Error("Forward queue worker stopped", zap.Error(err))
			}
		}()

		<-ctx.Done()

		// No new submissions may enter the group once Wait begins.
		<-dispatchDone
		if intentHost != nil {
			_ = intentHost.Stop(context.Background())
		}
		if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			logctx.From(ctx).Error("Submission goroutine error", zap.Error(err))
		}
		<-updatesDone
		// The pool remains owned until all forward transport calls have returned.
		<-forwardDone

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
