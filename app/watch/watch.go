package watch

import (
	"context"
	"fmt"
	"log/slog"
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
	messageLinks   <-chan messageLinkSubmission
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.From(ctx)
	account := types.AccountID(cfg.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	ctx = logctx.With(ctx, logctx.From(ctx).With(zap.String("component", "account.telegram"), zap.String("account", string(account))))
	if (opts.Download || opts.FeatureFlags != nil) && (opts.DownloadRouting == nil || opts.DownloadPipeline == nil) {
		return errors.New("download routing and pipeline services are required")
	}
	if opts.FeatureFlags == nil && !opts.Download && !opts.Forward {
		return errors.New("watch has no enabled work: enable trigger.download or trigger.forward")
	}
	if opts.Forward && strings.TrimSpace(cfg.Forward.Target) == "" {
		logctx.From(ctx).Info("转发目标未设置，将使用收藏夹", zap.String("component", "trigger.forward"))
	}
	if opts.Forward && len(cfg.Forward.Listen) == 0 {
		logctx.From(ctx).Warn("转发监听来源为空", zap.String("component", "trigger.forward"))
	}
	opts.FileSizeMinMB, opts.FileSizeMaxMB, _ = config.NormalizeFileSizeRange(opts.FileSizeMinMB, opts.FileSizeMaxMB)
	if opts.Filter == nil || opts.Naming == nil || opts.ForwardQueue == nil || opts.HTTPService == nil {
		return errors.New("watch requires runtime policy, forward and HTTP services")
	}
	opts.Account = types.AccountID(cfg.Namespace)
	if opts.Account == "" {
		opts.Account = types.DefaultAccount
	}
	opts.Limit = effectiveWatchOptionLimit(opts.Limit, cfg)
	opts.PoolSize = effectiveWatchOptionPoolSize(opts.PoolSize, cfg)

	kvd, err := kv.From(ctx).Open(cfg.Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}

	parentCtx := ctx
	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(parentCtx))
	defer cancelRun()

	signalCtx, stopSignalNotify := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignalNotify()

	runtime := newWatchRuntime(opts, kvd, logctx.From(runCtx))
	var pauseOnShutdownOnce sync.Once
	pauseOnShutdown := func() {
		pauseOnShutdownOnce.Do(func() {
			logctx.From(runCtx).Info("正在停止 Telegram 监听", zap.String("component", "account.telegram"))
			color.Yellow("⏹ Stopping watcher...")
			if (opts.Download || opts.FeatureFlags != nil) && runtime.worker != nil {
				paused, err := runtime.worker.PauseForShutdown(runCtx)
				if err != nil {
					logctx.From(runCtx).Warn("停止前暂停本地下载失败", zap.String("component", "downloader.local"), zap.Error(err))
					return
				}
				if len(paused) > 0 {
					logctx.From(runCtx).Info("停止前已暂停本地下载", zap.String("component", "downloader.local"), zap.Int("count", len(paused)))
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

	if opts.Download && opts.Forward {
		color.Green("👀 Watching for reactions and forward sources... Press Ctrl+C to stop")
	} else if opts.Forward {
		color.Green("👀 Watching forward sources... Press Ctrl+C to stop")
	} else {
		color.Green("👀 Watching for reactions... Press Ctrl+C to stop")
	}
	logctx.From(runCtx).Info("Telegram 监听已启动",
		zap.Bool("download_enabled", opts.Download), zap.Bool("forward_enabled", opts.Forward),
		zap.Strings("download_executors", cfg.Downloader.Executors))
	logctx.From(runCtx).Debug("Telegram 下载并发设置",
		zap.Int("dc_pool_size", opts.PoolSize), zap.Int("max_concurrent_downloads", opts.Limit))

	if opts.Download && (config.UsesDownloadExecutor(cfg, config.DownloadExecutorAria2) || config.UsesDownloadExecutor(cfg, config.DownloadExecutorHTTP)) {
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

		logctx.From(runCtx).Warn("Telegram 监听已断开，稍后重连", zap.String("component", "account.telegram"), zap.Duration("retry_interval", reconnectDelay), zap.Error(err))

		select {
		case <-runCtx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func runOnce(ctx context.Context, opts Options, kvd storage.Storage, reconnectDelay time.Duration, runtime *watchRuntime) (rerr error) {
	cfg := config.From(ctx)
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
		opts:         opts,
		runtime:      runtime,
		messageLinks: opts.messageLinks,
	}

	if _, err := w.reactionPolicy(ctx); err != nil {
		return err
	}

	// Register reaction handlers whenever download or forward is enabled. Forward
	// reacts on its trigger emoji (or any emoji when its trigger set is empty),
	// so it needs these handlers regardless of how many triggers are configured.
	if opts.Download || opts.Forward || opts.FeatureFlags != nil {
		d.OnMessageReactions(w.onReaction)
		d.OnEditMessage(w.onEditMessage)
		d.OnEditChannelMessage(w.onEditChannelMessage)
	}
	if opts.Forward || opts.FeatureFlags != nil {
		d.OnNewMessage(w.onNewMessageForward)
		d.OnNewChannelMessage(w.onNewChannelMessageForward)
	}
	d.OnFallback(func(ctx context.Context, e tg.Entities, update tg.UpdateClass) error {
		updateType := fmt.Sprintf("%T", update)
		logctx.From(ctx).Debug("Unhandled update received",
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
		pool := dcpool.NewResizable(client.Client,
			int64(poolSize),
			tclient.NewDefaultMiddlewares(ctx, reconnectDelay)...)
		defer multierr.AppendInvoke(&rerr, multierr.Close(pool))
		defer runtime.pools.Set(nil)

		runtime.pools.Set(pool)

		w.pool = pool
		w.manager = peers.Options{Storage: peerStore}.Build(pool.Default(ctx))

		self, err := client.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "get self user")
		}
		if (opts.Download || opts.FeatureFlags != nil) && runtime.worker != nil {
			host, executor, err := application.LocalDownloadHost(ctx, opts.Account, runtime.worker, opts.ComponentStore)
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
		var intentHost *rte.Runtime
		if opts.Download || opts.Forward || opts.FeatureFlags != nil {
			var download ports.DownloadIntentHandler
			if opts.Download || opts.FeatureFlags != nil {
				download = func(intentCtx context.Context, request types.DownloadIntent) error {
					_, err := w.processDownloadIntent(intentCtx, request)
					return err
				}
			}
			var forward ports.ForwardIntentHandler
			if opts.Forward || opts.FeatureFlags != nil {
				forward = w.processForwardIntent
			}
			var port ports.DownloadIntents
			var forwardPort ports.ForwardIntents
			intentHost, port, forwardPort, err = application.IntentHostStored(ctx, opts.Account, download, forward, opts.ComponentStore, w.processDownloadIntent)
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
		// Bind the forwarding component before accepting Telegram updates. Its
		// router and worker share this connection and are drained before the pool.
		forwardCtx, forwardCancel := context.WithCancel(ctx)
		forwardReady, forwardDone := make(chan struct{}), make(chan struct{})
		var forwardErr error
		go func() {
			defer close(forwardDone)
			forwardErr = opts.ForwardQueue.Serve(forwardCtx, appforward.Runtime{
				Pool: pool, Manager: w.manager, PoolSize: opts.PoolSize, Account: opts.Account,
				ComponentStore: opts.ComponentStore, Rules: opts.ForwardRules, Listening: watchForwardListening{w},
				OnReady: func() { close(forwardReady) },
			})
			if forwardErr != nil && !errors.Is(forwardErr, context.Canceled) {
				logctx.From(ctx).Error("Forward queue worker stopped", zap.Error(forwardErr))
			}
		}()
		defer func() { forwardCancel(); <-forwardDone }()
		select {
		case <-forwardReady:
		case <-forwardDone:
			if forwardErr != nil {
				return forwardErr
			}
			return errors.New("forward connection stopped before binding")
		case <-ctx.Done():
			return ctx.Err()
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
		if opts.Download || opts.FeatureFlags != nil {
			go func() {
				defer close(dispatchDone)
				w.dispatcher(ctx)
			}()
		} else {
			close(dispatchDone)
		}

		<-ctx.Done()

		// Both request adapters and the component consumer drain submissions
		// before this connection's pool is released.
		<-dispatchDone
		if intentHost != nil {
			_ = intentHost.Stop(context.Background())
		}
		<-updatesDone
		// The pool remains owned until all forward transport calls have returned.
		<-forwardDone

		return nil
	})
	return err
}

func warnPublicBaseURL(base string) {
	u, err := url.Parse(base)
	if err != nil {
		return
	}

	switch u.Hostname() {
	case "0.0.0.0", "::":
		slog.Warn("下载公网地址使用未指定地址，外部下载器可能无法访问", "component", "proxy.range", "host", u.Hostname())
	case "localhost":
		slog.Warn("下载公网地址使用 localhost，仅适合同机下载器", "component", "proxy.range")
	default:
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			slog.Warn("下载公网地址使用回环地址，仅适合同机下载器", "component", "proxy.range", "host", u.Hostname())
		}
	}
}
