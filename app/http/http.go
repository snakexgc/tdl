package httpdl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/application"
	rangeproxy "github.com/snakexgc/tdl/application/proxy.range"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	transfer "github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

const (
	downloadStreamPartSize            = 1024 * 1024
	telegramGetFilePreciseAlignment   = 1024
	telegramGetFileFragmentWindowSize = 1024 * 1024
	httpReadHeaderTimeout             = 10 * time.Second
	httpIdleTimeout                   = 2 * time.Minute
	httpMaxHeaderBytes                = 1 << 20
	telegramClientWaitTimeout         = 30 * time.Second
	httpDeliveryPersistTimeout        = 5 * time.Second
)

const (
	// telegramChunkMaxRetries bounds the last-resort, in-place retry of a single
	// upload.getFile chunk for transient conditions the lower MTProto layers do
	// not already recover (an empty body, a hung request, a connection reset that
	// survived the retry middleware). Recovering the chunk here keeps the HTTP
	// response alive instead of tearing down the whole stream and forcing the
	// client (aria2) to re-download from the start.
	telegramChunkMaxRetries     = 4
	telegramChunkRetryBaseDelay = 250 * time.Millisecond
	telegramChunkRetryMaxDelay  = 2 * time.Second
	// telegramChunkAttemptTimeout is a dead-connection backstop, NOT a throughput
	// throttle: a single ≤1 MiB getFile slower than this means < ~3.5 KiB/s, which
	// is below any usable link, so the connection is effectively dead. It is
	// deliberately far above any real per-chunk transfer time so it can never cut
	// off a slow-but-progressing download and shorten the resulting file.
	telegramChunkAttemptTimeout = 5 * time.Minute
)

const (
	mediaKindDocument = "document"
	mediaKindPhoto    = "photo"
)

const (
	downloadTaskKeyPrefix  = taskhub.LinkPrefix
	downloadTaskIndexKey   = taskhub.LinkIndex
	defaultDownloadTaskTTL = 24 * time.Hour
	sourceRegistryIdleTTL  = 2 * time.Minute
	telegramFileErrorTTL   = time.Minute
)

const (
	DownloadTaskKeyPrefix = downloadTaskKeyPrefix
	DownloadTaskIndexKey  = downloadTaskIndexKey
)

type Task = downloadTask

type TaskStore = taskStore

type PoolHolder = poolHolder

type Proxy = downloadProxy

type TaskStreamer = taskStreamer

type downloadRange struct {
	start int64
	end   int64
}

type downloadProxy struct {
	account        types.AccountID
	rangeHost      *rte.Runtime
	rangeHandler   http.Handler
	componentStore *rteconfig.Store
	cfgMu          sync.RWMutex
	cfg            config.HTTPConfig
	tasks          *taskStore
	pools          *poolHolder
	sources        *sourceRegistry
	server         *http.Server
	stream         taskStreamer
	parallel       taskStreamer
	scheduler      *transfer.Scheduler
	logger         *zap.Logger

	reporterMu sync.RWMutex
	reporter   TelegramFileErrorReporter

	clientWaitTimeout time.Duration
}

func newDownloadProxy(cfg config.HTTPConfig, maxFiles, poolSize int, pools *poolHolder, kv storage.Storage, logger *zap.Logger) *downloadProxy {
	if logger == nil {
		logger = zap.NewNop()
	}
	if maxFiles < 1 {
		maxFiles = config.DefaultConfig().Limit
	}
	if poolSize < 1 {
		poolSize = config.DefaultConfig().PoolSize
	}
	if pools == nil {
		pools = &poolHolder{}
	}

	p := &downloadProxy{
		cfg:               cfg,
		tasks:             newTaskStore(kv, downloadLinkTTL(cfg)),
		pools:             pools,
		sources:           newSourceRegistry(),
		scheduler:         transfer.NewScheduler(maxFiles, poolSize),
		logger:            logger.Named("http-download").With(zap.String("component", "proxy.range")),
		clientWaitTimeout: telegramClientWaitTimeout,
	}

	p.stream = p.streamTask
	p.parallel = p.streamTaskParallel
	setActiveScheduler(p.scheduler)
	p.server = p.newServer()

	return p
}

func (p *downloadProxy) newServer() *http.Server {
	cfg := p.config()
	return &http.Server{
		Addr:              config.HTTPConfigListenAddr(cfg),
		Handler:           p.routes(),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

func (p *downloadProxy) config() config.HTTPConfig {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.cfg
}

func (p *downloadProxy) updateConfig(cfg config.HTTPConfig) bool {
	p.cfgMu.Lock()
	previousListen := config.HTTPConfigListenAddr(p.cfg)
	p.cfg = cfg
	nextListen := config.HTTPConfigListenAddr(p.cfg)
	p.cfgMu.Unlock()
	return previousListen != nextListen
}

func NewProxy(cfg config.HTTPConfig, maxFiles, poolSize int, pools *PoolHolder, kv storage.Storage, logger *zap.Logger) *Proxy {
	return newDownloadProxy(cfg, maxFiles, poolSize, pools, kv, logger)
}

// SetComponentStore is called by the composition root before the proxy starts.
func (p *downloadProxy) SetComponentStore(store *rteconfig.Store) { p.componentStore = store }

func (p *downloadProxy) Tasks() *TaskStore {
	if p == nil {
		return nil
	}
	return p.tasks
}

func (p *downloadProxy) Scheduler() *transfer.Scheduler {
	if p == nil {
		return nil
	}
	return p.scheduler
}

func (p *downloadProxy) SetTaskTTL(ttl time.Duration) {
	if p == nil || p.tasks == nil {
		return
	}
	p.tasks.SetTTL(ttl)
}

func (p *downloadProxy) SetStream(stream TaskStreamer) {
	if p == nil {
		return
	}
	p.stream = stream
	p.parallel = stream
}

func (p *downloadProxy) SetTelegramFileErrorReporter(reporter TelegramFileErrorReporter) {
	if p == nil {
		return
	}

	p.reporterMu.Lock()
	defer p.reporterMu.Unlock()

	p.reporter = reporter
}

func (p *downloadProxy) telegramFileErrorReporter() TelegramFileErrorReporter {
	if p == nil {
		return nil
	}

	p.reporterMu.RLock()
	defer p.reporterMu.RUnlock()

	return p.reporter
}

func (p *downloadProxy) Stream(ctx context.Context, task *Task, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	if p == nil {
		return errors.New("download proxy is not initialized")
	}
	if p.stream != nil {
		return p.stream(ctx, task, lease, start, end, w)
	}
	return p.streamTask(ctx, task, lease, start, end, w)
}

func (p *downloadProxy) StreamParallel(ctx context.Context, task *Task, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	if p == nil {
		return errors.New("download proxy is not initialized")
	}
	if p.parallel != nil {
		return p.parallel(ctx, task, lease, start, end, w)
	}
	return p.streamTaskParallel(ctx, task, lease, start, end, w)
}

func (p *downloadProxy) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	handler := rangeproxy.New(rangeSource{proxy: p}, p.logger, p.clientWaitTimeout)
	host, err := application.RangeHost(runCtx, p.account, handler, p.componentStore)
	if err != nil {
		return err
	}
	p.cfgMu.Lock()
	p.rangeHost = host
	p.rangeHandler = handler
	p.cfgMu.Unlock()
	defer func() { _ = host.Stop(context.Background()) }()
	server := p.newServer()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-runCtx.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
	}()
	err = server.ListenAndServe()
	cancel()
	<-shutdownDone
	return err
}

func (p *downloadProxy) Host() *rte.Runtime {
	if p == nil {
		return nil
	}
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.rangeHost
}

func (p *downloadProxy) CleanupExpiredTasks(ctx context.Context) error {
	return p.tasks.CleanupExpired(ctx, time.Now())
}

func (p *downloadProxy) NewTask(ctx context.Context, peerID int64, msgID int, peer tg.InputPeerClass, fileName string, fileSize int64, media *tmedia.Media) (*downloadTask, error) {
	id, err := downloadTaskID(media)
	if err != nil {
		return nil, errors.Wrap(err, "build persistent download task id")
	}

	now := time.Now()
	task := &downloadTask{
		ID:           id,
		PeerID:       peerID,
		MessageID:    msgID,
		Peer:         peer,
		FileName:     fileName,
		FileSize:     fileSize,
		Media:        media,
		CreatedAt:    now,
		LastActiveAt: now,
	}
	if err := p.tasks.Add(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

func (p *downloadProxy) BuildURL(taskID string) (string, error) {
	return buildDownloadURL(p.config().PublicBaseURL, taskID)
}

func (p *downloadProxy) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/download/", p.handleDownload)
	return mux
}

func (p *downloadProxy) streamTask(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	return p.streamTaskWithMode(ctx, task, lease, start, end, w, false)
}

func (p *downloadProxy) streamTaskParallel(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	return p.streamTaskWithMode(ctx, task, lease, start, end, w, true)
}

func (p *downloadProxy) streamTaskWithMode(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer, parallel bool) error {
	pool := p.pools.Get()
	if pool == nil {
		err := errors.New("telegram client unavailable")
		p.logger.Debug("Cannot stream download task",
			zap.String("task_id", task.ID),
			zap.Error(err))
		return err
	}

	streamCtx := logctx.With(ctx, p.logger.With(
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("file_size", task.FileSize),
		zap.Int64("range_start", start),
		zap.Int64("range_end", end),
		zap.Int("dc_capacity", lease.Capacity()),
	))

	refresh := func(ctx context.Context) (*tmedia.Media, error) {
		p.logger.Debug("Refreshing expired Telegram file reference",
			zap.String("task_id", task.ID),
			zap.Int64("peer_id", task.PeerID),
			zap.Int("msg_id", task.MessageID))
		if err := p.refreshTaskMedia(ctx, task); err != nil {
			return nil, errors.Wrap(err, "refresh expired file reference")
		}
		refreshed, ok, err := p.tasks.Get(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("download task disappeared after media refresh")
		}
		return refreshed.Media, nil
	}

	handle := p.sources.Acquire(task, refresh)
	defer handle.Release()
	if parallel {
		return streamTelegramMediaParallel(streamCtx, pool, handle.Source(), lease, p.telegramFileErrorReporter(), start, end, w)
	}
	return streamTelegramMedia(streamCtx, pool, handle.Source(), lease, p.telegramFileErrorReporter(), start, end, w)
}

func (p *downloadProxy) refreshTaskMedia(ctx context.Context, task *downloadTask) error {
	if task.Peer == nil {
		return errors.New("download task peer is empty")
	}

	pool := p.pools.Get()
	if pool == nil {
		return errors.New("telegram client unavailable")
	}

	msg, err := tutil.GetSingleMessage(ctx, pool.Default(ctx), task.Peer, task.MessageID)
	if err != nil {
		return errors.Wrap(err, "get message for media refresh")
	}
	media, ok := tmedia.GetMedia(msg)
	if !ok {
		return errors.New("message no longer has media")
	}
	if task.Media != nil && media.DC != task.Media.DC {
		return fmt.Errorf("refreshed media changed dc from %d to %d", task.Media.DC, media.DC)
	}
	id, err := downloadTaskID(media)
	if err != nil {
		return err
	}
	if id != task.ID {
		return fmt.Errorf("refreshed media id changed from %q to %q", task.ID, id)
	}

	refreshed := *task
	refreshed.Media = media
	refreshed.FileSize = media.Size
	if err := p.tasks.Add(ctx, &refreshed); err != nil {
		return err
	}
	return nil
}

func isRefreshableFileReferenceError(err error) bool {
	if tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID", "FILE_REFERENCE_EMPTY", "FILEREF_UPGRADE_NEEDED") {
		return true
	}

	rpcErr, ok := tgerr.As(err)
	return ok && strings.HasPrefix(rpcErr.Type, "FILE_REFERENCE_")
}

func downloadTaskID(media *tmedia.Media) (string, error) {
	location, err := persistentMediaLocationFromMedia(media)
	if err != nil {
		return "", err
	}

	switch location.Kind {
	case mediaKindDocument:
		if location.ThumbSize != "" {
			return fmt.Sprintf("document_%d_%s", location.ID, safeTaskIDPart(location.ThumbSize)), nil
		}
		return fmt.Sprintf("document_%d", location.ID), nil
	case mediaKindPhoto:
		if location.ThumbSize != "" {
			return fmt.Sprintf("photo_%d_%s", location.ID, safeTaskIDPart(location.ThumbSize)), nil
		}
		return fmt.Sprintf("photo_%d", location.ID), nil
	default:
		return "", fmt.Errorf("unsupported media location kind %q", location.Kind)
	}
}

func safeTaskIDPart(v string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(v)
}

func downloadTaskStorageKey(id string) string {
	return downloadTaskKeyPrefix + id
}

func TaskStorageKey(id string) string {
	return downloadTaskStorageKey(id)
}

func buildDownloadURL(baseURL, taskID string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "parse public_base_url")
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("public_base_url must include scheme and host")
	}

	u.Path = path.Join(strings.TrimSuffix(u.Path, "/"), "download", taskID)

	return u.String(), nil
}

func downloadLinkTTL(cfg config.HTTPConfig) time.Duration {
	if cfg.DownloadLinkTTLHours <= 0 {
		return 0
	}
	return time.Duration(cfg.DownloadLinkTTLHours) * time.Hour
}

func LinkTTL(cfg config.HTTPConfig) time.Duration {
	return downloadLinkTTL(cfg)
}
