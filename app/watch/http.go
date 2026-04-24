package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
)

const (
	downloadStreamPartSize            = 1024 * 1024
	telegramGetFilePreciseAlignment   = 1024
	telegramGetFileFragmentWindowSize = 1024 * 1024
)

const (
	downloadTaskKeyPrefix = "watch.download."
	downloadTaskIndexKey  = "watch.download.index"
	downloadTaskTTL       = 24 * time.Hour
)

type downloadTask struct {
	ID        string
	PeerID    int64
	MessageID int
	Peer      tg.InputPeerClass
	FileName  string
	FileSize  int64
	Media     *tmedia.Media
	CreatedAt time.Time
}

type persistentDownloadTask struct {
	ID        string                  `json:"id"`
	PeerID    int64                   `json:"peer_id"`
	MessageID int                     `json:"message_id"`
	Peer      persistentInputPeer     `json:"peer"`
	FileName  string                  `json:"file_name"`
	FileSize  int64                   `json:"file_size"`
	Media     persistentDownloadMedia `json:"media"`
	CreatedAt time.Time               `json:"created_at"`
}

type persistentDownloadMedia struct {
	Name     string                  `json:"name"`
	Size     int64                   `json:"size"`
	DC       int                     `json:"dc"`
	Date     int64                   `json:"date"`
	Location persistentMediaLocation `json:"location"`
}

type persistentMediaLocation struct {
	Kind          string `json:"kind"`
	ID            int64  `json:"id"`
	AccessHash    int64  `json:"access_hash"`
	FileReference []byte `json:"file_reference"`
	ThumbSize     string `json:"thumb_size,omitempty"`
}

type persistentInputPeer struct {
	Kind       string `json:"kind,omitempty"`
	ID         int64  `json:"id,omitempty"`
	AccessHash int64  `json:"access_hash,omitempty"`
}

type persistentDownloadTaskIndex map[string]time.Time

func persistentDownloadTaskFromTask(task *downloadTask) (persistentDownloadTask, error) {
	if task == nil || task.Media == nil {
		return persistentDownloadTask{}, errors.New("download task media is empty")
	}
	location, err := persistentMediaLocationFromMedia(task.Media)
	if err != nil {
		return persistentDownloadTask{}, err
	}
	peer, err := persistentInputPeerFromPeer(task.Peer)
	if err != nil {
		return persistentDownloadTask{}, err
	}

	return persistentDownloadTask{
		ID:        task.ID,
		PeerID:    task.PeerID,
		MessageID: task.MessageID,
		Peer:      peer,
		FileName:  task.FileName,
		FileSize:  task.FileSize,
		Media: persistentDownloadMedia{
			Name:     task.Media.Name,
			Size:     task.Media.Size,
			DC:       task.Media.DC,
			Date:     task.Media.Date,
			Location: location,
		},
		CreatedAt: task.CreatedAt,
	}, nil
}

func (p persistentDownloadTask) ToTask() (*downloadTask, error) {
	media, err := p.Media.ToMedia()
	if err != nil {
		return nil, err
	}
	peer, err := p.Peer.ToInputPeer()
	if err != nil {
		return nil, err
	}

	return &downloadTask{
		ID:        p.ID,
		PeerID:    p.PeerID,
		MessageID: p.MessageID,
		Peer:      peer,
		FileName:  p.FileName,
		FileSize:  p.FileSize,
		Media:     media,
		CreatedAt: p.CreatedAt,
	}, nil
}

func (p persistentDownloadMedia) ToMedia() (*tmedia.Media, error) {
	location, err := p.Location.ToInputFileLocation()
	if err != nil {
		return nil, err
	}

	return &tmedia.Media{
		InputFileLoc: location,
		Name:         p.Name,
		Size:         p.Size,
		DC:           p.DC,
		Date:         p.Date,
	}, nil
}

func persistentMediaLocationFromMedia(media *tmedia.Media) (persistentMediaLocation, error) {
	if media == nil || media.InputFileLoc == nil {
		return persistentMediaLocation{}, errors.New("media location is empty")
	}

	switch loc := media.InputFileLoc.(type) {
	case *tg.InputDocumentFileLocation:
		return persistentMediaLocation{
			Kind:          "document",
			ID:            loc.ID,
			AccessHash:    loc.AccessHash,
			FileReference: loc.FileReference,
			ThumbSize:     loc.ThumbSize,
		}, nil
	case *tg.InputPhotoFileLocation:
		return persistentMediaLocation{
			Kind:          "photo",
			ID:            loc.ID,
			AccessHash:    loc.AccessHash,
			FileReference: loc.FileReference,
			ThumbSize:     loc.ThumbSize,
		}, nil
	default:
		return persistentMediaLocation{}, fmt.Errorf("unsupported media location %T", media.InputFileLoc)
	}
}

func (p persistentMediaLocation) ToInputFileLocation() (tg.InputFileLocationClass, error) {
	switch p.Kind {
	case "document":
		return &tg.InputDocumentFileLocation{
			ID:            p.ID,
			AccessHash:    p.AccessHash,
			FileReference: p.FileReference,
			ThumbSize:     p.ThumbSize,
		}, nil
	case "photo":
		return &tg.InputPhotoFileLocation{
			ID:            p.ID,
			AccessHash:    p.AccessHash,
			FileReference: p.FileReference,
			ThumbSize:     p.ThumbSize,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported media location kind %q", p.Kind)
	}
}

func persistentInputPeerFromPeer(peer tg.InputPeerClass) (persistentInputPeer, error) {
	switch p := peer.(type) {
	case nil:
		return persistentInputPeer{}, nil
	case *tg.InputPeerUser:
		return persistentInputPeer{Kind: "user", ID: p.UserID, AccessHash: p.AccessHash}, nil
	case *tg.InputPeerChannel:
		return persistentInputPeer{Kind: "channel", ID: p.ChannelID, AccessHash: p.AccessHash}, nil
	case *tg.InputPeerChat:
		return persistentInputPeer{Kind: "chat", ID: p.ChatID}, nil
	default:
		return persistentInputPeer{}, fmt.Errorf("unsupported input peer %T", peer)
	}
}

func (p persistentInputPeer) ToInputPeer() (tg.InputPeerClass, error) {
	switch p.Kind {
	case "":
		return nil, nil
	case "user":
		return &tg.InputPeerUser{UserID: p.ID, AccessHash: p.AccessHash}, nil
	case "channel":
		return &tg.InputPeerChannel{ChannelID: p.ID, AccessHash: p.AccessHash}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: p.ID}, nil
	default:
		return nil, fmt.Errorf("unsupported input peer kind %q", p.Kind)
	}
}

type taskStore struct {
	mu    sync.RWMutex
	tasks map[string]*downloadTask
	kv    storage.Storage
}

func newTaskStore(kv storage.Storage) *taskStore {
	return &taskStore{
		tasks: make(map[string]*downloadTask),
		kv:    kv,
	}
}

func (s *taskStore) Add(ctx context.Context, task *downloadTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.kv != nil {
		if err := s.cleanupExpiredLocked(ctx, time.Now()); err != nil {
			return errors.Wrap(err, "cleanup expired download tasks")
		}

		persisted, err := persistentDownloadTaskFromTask(task)
		if err != nil {
			return errors.Wrap(err, "create persistent download task")
		}
		data, err := json.Marshal(persisted)
		if err != nil {
			return errors.Wrap(err, "marshal persistent download task")
		}
		if err := s.kv.Set(ctx, downloadTaskStorageKey(task.ID), data); err != nil {
			return errors.Wrap(err, "persist download task")
		}
		if err := s.addIndexEntryLocked(ctx, task.ID, task.CreatedAt); err != nil {
			return errors.Wrap(err, "index persistent download task")
		}
	}

	s.tasks[task.ID] = task
	return nil
}

func (s *taskStore) Get(ctx context.Context, id string) (*downloadTask, bool, error) {
	s.mu.RLock()
	task, ok := s.tasks[id]
	s.mu.RUnlock()
	if ok {
		if isDownloadTaskExpired(task.CreatedAt, time.Now()) {
			if err := s.delete(ctx, id); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		}
		return task, true, nil
	}

	if s.kv == nil {
		return nil, false, nil
	}

	data, err := s.kv.Get(ctx, downloadTaskStorageKey(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, errors.Wrap(err, "load persistent download task")
	}

	var persisted persistentDownloadTask
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, false, errors.Wrap(err, "decode persistent download task")
	}
	if isDownloadTaskExpired(persisted.CreatedAt, time.Now()) {
		if err := s.delete(ctx, id); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	task, err = persisted.ToTask()
	if err != nil {
		return nil, false, errors.Wrap(err, "restore persistent download task")
	}

	s.mu.Lock()
	s.tasks[id] = task
	s.mu.Unlock()

	return task, true, nil
}

func (s *taskStore) CleanupExpired(ctx context.Context, now time.Time) error {
	if s.kv == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cleanupExpiredLocked(ctx, now)
}

func (s *taskStore) cleanupExpiredLocked(ctx context.Context, now time.Time) error {
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}

	changed := false
	for id, createdAt := range index {
		if !isDownloadTaskExpired(createdAt, now) {
			continue
		}
		if err := s.kv.Delete(ctx, downloadTaskStorageKey(id)); err != nil {
			return errors.Wrap(err, "delete expired download task")
		}
		delete(index, id)
		delete(s.tasks, id)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveIndex(ctx, index)
}

func (s *taskStore) addIndexEntryLocked(ctx context.Context, id string, createdAt time.Time) error {
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	index[id] = createdAt
	return s.saveIndex(ctx, index)
}

func (s *taskStore) delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteLocked(ctx, id)
}

func (s *taskStore) deleteLocked(ctx context.Context, id string) error {
	if s.kv != nil {
		if err := s.kv.Delete(ctx, downloadTaskStorageKey(id)); err != nil {
			return errors.Wrap(err, "delete persistent download task")
		}
		index, err := s.loadIndex(ctx)
		if err != nil {
			return err
		}
		delete(index, id)
		if err := s.saveIndex(ctx, index); err != nil {
			return err
		}
	}

	delete(s.tasks, id)
	return nil
}

func (s *taskStore) loadIndex(ctx context.Context) (persistentDownloadTaskIndex, error) {
	data, err := s.kv.Get(ctx, downloadTaskIndexKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return persistentDownloadTaskIndex{}, nil
		}
		return nil, errors.Wrap(err, "load download task index")
	}

	var index persistentDownloadTaskIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, errors.Wrap(err, "decode download task index")
	}
	if index == nil {
		index = persistentDownloadTaskIndex{}
	}
	return index, nil
}

func (s *taskStore) saveIndex(ctx context.Context, index persistentDownloadTaskIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return errors.Wrap(err, "marshal download task index")
	}
	if err := s.kv.Set(ctx, downloadTaskIndexKey, data); err != nil {
		return errors.Wrap(err, "save download task index")
	}
	return nil
}

func isDownloadTaskExpired(createdAt, now time.Time) bool {
	if createdAt.IsZero() {
		return false
	}
	return !createdAt.Add(downloadTaskTTL).After(now)
}

type poolHolder struct {
	mu   sync.RWMutex
	pool dcpool.Pool
}

func (h *poolHolder) Set(pool dcpool.Pool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.pool = pool
}

func (h *poolHolder) Get() dcpool.Pool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.pool
}

type taskStreamer func(ctx context.Context, task *downloadTask, lease *downloadLease, start, end int64, w io.Writer) error

type telegramMediaSource struct {
	mu        sync.RWMutex
	media     *tmedia.Media
	refresh   func(ctx context.Context) (*tmedia.Media, error)
	refreshMu sync.Mutex
}

type downloadProxy struct {
	cfg     config.HTTPConfig
	tasks   *taskStore
	pools   *poolHolder
	server  *http.Server
	stream  taskStreamer
	limiter *downloadLimiter
	logger  *zap.Logger
}

func newDownloadProxy(cfg config.HTTPConfig, maxFiles, maxPerFile int, pools *poolHolder, kv storage.Storage, logger *zap.Logger) *downloadProxy {
	if logger == nil {
		logger = zap.NewNop()
	}

	p := &downloadProxy{
		cfg:     cfg,
		tasks:   newTaskStore(kv),
		pools:   pools,
		limiter: newDownloadLimiter(maxFiles, maxPerFile),
		logger:  logger.Named("watch-http"),
	}

	p.stream = p.streamTask
	p.server = &http.Server{
		Addr:    cfg.Listen,
		Handler: p.routes(),
	}

	return p
}

func (p *downloadProxy) Start(ctx context.Context) error {
	p.logger.Info("Starting HTTP download proxy",
		zap.String("listen", p.cfg.Listen),
		zap.String("public_base_url", p.cfg.PublicBaseURL))

	p.startCleanupLoop(ctx)

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.server.Shutdown(shutdownCtx)
	}()

	return p.server.ListenAndServe()
}

func (p *downloadProxy) CleanupExpiredTasks(ctx context.Context) error {
	return p.tasks.CleanupExpired(ctx, time.Now())
}

func (p *downloadProxy) startCleanupLoop(ctx context.Context) {
	if p.tasks.kv == nil {
		return
	}

	cleanup := func() {
		if err := p.CleanupExpiredTasks(ctx); err != nil && !errors.Is(err, context.Canceled) {
			p.logger.Warn("Failed to clean expired download tasks", zap.Error(err))
		}
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

func (p *downloadProxy) NewTask(ctx context.Context, peerID int64, msgID int, peer tg.InputPeerClass, fileName string, fileSize int64, media *tmedia.Media) (*downloadTask, error) {
	id, err := downloadTaskID(media)
	if err != nil {
		return nil, errors.Wrap(err, "build persistent download task id")
	}

	task := &downloadTask{
		ID:        id,
		PeerID:    peerID,
		MessageID: msgID,
		Peer:      peer,
		FileName:  fileName,
		FileSize:  fileSize,
		Media:     media,
		CreatedAt: time.Now(),
	}
	if err := p.tasks.Add(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

func (p *downloadProxy) BuildURL(taskID string) (string, error) {
	return buildDownloadURL(p.cfg.PublicBaseURL, taskID)
}

func (p *downloadProxy) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/download/", p.handleDownload)
	return mux
}

func (p *downloadProxy) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/download/")
	if taskID == "" || strings.Contains(taskID, "/") {
		p.logger.Warn("Rejecting invalid download path",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("user_agent", r.UserAgent()))
		http.NotFound(w, r)
		return
	}

	p.logger.Info("Download request received",
		zap.String("method", r.Method),
		zap.String("task_id", taskID),
		zap.String("range", r.Header.Get("Range")),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("user_agent", r.UserAgent()))

	task, ok, err := p.tasks.Get(r.Context(), taskID)
	if err != nil {
		p.logger.Error("Failed to load download task",
			zap.String("task_id", taskID),
			zap.Error(err))
		http.Error(w, "failed to load download task", http.StatusInternalServerError)
		return
	}
	if !ok {
		p.logger.Warn("Download task not found",
			zap.String("task_id", taskID))
		http.NotFound(w, r)
		return
	}

	var lease *downloadLease
	if r.Method != http.MethodHead {
		waitStart := time.Now()
		acquired, err := p.limiter.Acquire(r.Context(), task.ID)
		if err != nil {
			fields := []zap.Field{
				zap.String("task_id", task.ID),
				zap.String("file_name", task.FileName),
				zap.Duration("waited", time.Since(waitStart)),
				zap.Error(err),
			}
			if errors.Is(err, context.Canceled) {
				p.logger.Warn("Download request canceled while waiting for slot", fields...)
				return
			}

			p.logger.Error("Failed to acquire download slot", fields...)
			http.Error(w, "failed to acquire download slot", http.StatusInternalServerError)
			return
		}
		lease = acquired
		defer lease.Release()

		if waited := time.Since(waitStart); waited >= 100*time.Millisecond {
			p.logger.Info("Download request waited for slot",
				zap.String("task_id", task.ID),
				zap.String("file_name", task.FileName),
				zap.Duration("waited", waited))
		}
	}

	start, end, partial, err := parseDownloadRange(r.Header.Get("Range"), task.FileSize)
	if err != nil {
		p.logger.Warn("Invalid download range",
			zap.String("task_id", taskID),
			zap.String("range", r.Header.Get("Range")),
			zap.Error(err))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", task.FileSize))
		http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(task.FileName))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": task.FileName})

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))

	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, task.FileSize))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	p.logger.Info("Serving download task",
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("file_size", task.FileSize),
		zap.Int64("range_start", start),
		zap.Int64("range_end", end),
		zap.Bool("partial", partial))

	if r.Method == http.MethodHead {
		p.logger.Info("HEAD request served without body",
			zap.String("task_id", task.ID))
		return
	}

	if err := p.stream(r.Context(), task, lease, start, end, w); err != nil {
		fields := []zap.Field{
			zap.String("task_id", task.ID),
			zap.String("file_name", task.FileName),
			zap.Int64("range_start", start),
			zap.Int64("range_end", end),
			zap.Error(err),
		}
		if errors.Is(err, context.Canceled) {
			p.logger.Warn("Download client disconnected", fields...)
			return
		}

		p.logger.Error("Download stream failed", fields...)
		return
	}

	p.logger.Info("Download stream finished",
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("range_start", start),
		zap.Int64("range_end", end))
}

func (p *downloadProxy) streamTask(ctx context.Context, task *downloadTask, lease *downloadLease, start, end int64, w io.Writer) error {
	pool := p.pools.Get()
	if pool == nil {
		err := errors.New("telegram client unavailable")
		p.logger.Error("Cannot stream download task",
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
		zap.Int("max_workers", lease.MaxWorkers()),
	))

	source := &telegramMediaSource{
		media: task.Media,
		refresh: func(ctx context.Context) (*tmedia.Media, error) {
			p.logger.Warn("Refreshing expired Telegram file reference",
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
		},
	}

	return streamTelegramMedia(streamCtx, pool, source, lease, start, end, w)
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
	case "document":
		if location.ThumbSize != "" {
			return fmt.Sprintf("document_%d_%s", location.ID, safeTaskIDPart(location.ThumbSize)), nil
		}
		return fmt.Sprintf("document_%d", location.ID), nil
	case "photo":
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

func streamTelegramMedia(ctx context.Context, pool dcpool.Pool, source *telegramMediaSource, lease *downloadLease, start, end int64, w io.Writer) error {
	logger := logctx.From(ctx)
	if end < start {
		return errors.New("invalid byte range")
	}

	jobs := buildDownloadChunkJobs(start, end)
	if len(jobs) == 0 {
		return nil
	}

	flusher, _ := w.(http.Flusher)
	workers := min(lease.MaxWorkers(), len(jobs))
	results := make(chan downloadChunkResult, workers)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logger.Info("Starting Telegram media stream",
		zap.Int("dc", source.Media().DC),
		zap.Int64("media_size", source.Media().Size),
		zap.Int64("start", start),
		zap.Int64("end", end),
		zap.Int("workers", workers),
		zap.Int("chunks", len(jobs)))

	g, gctx := errgroup.WithContext(ctx)
	jobsCh := make(chan downloadChunkJob)
	g.Go(func() error {
		defer close(jobsCh)
		for _, job := range jobs {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case jobsCh <- job:
			}
		}
		return nil
	})

	for i := 0; i < workers; i++ {
		g.Go(func() error {
			for {
				select {
				case <-gctx.Done():
					return gctx.Err()
				case job, ok := <-jobsCh:
					if !ok {
						return nil
					}

					data, err := source.FetchChunk(gctx, pool, lease, job.req)
					if err != nil {
						return err
					}
					data, err = sliceTelegramChunk(data, job.skip, job.take)
					if err != nil {
						return errors.Wrap(err, "slice telegram file chunk")
					}

					select {
					case <-gctx.Done():
						return gctx.Err()
					case results <- downloadChunkResult{index: job.index, data: data}:
					}
				}
			}
		})
	}

	done := make(chan error, 1)
	go func() {
		done <- g.Wait()
		close(results)
	}()

	pending := make(map[int][]byte, workers)
	var written int64
	next := 0
	for result := range results {
		pending[result.index] = result.data
		for {
			chunk, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)

			n, err := w.Write(chunk)
			written += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
			if err != nil {
				cancel()
				logger.Error("Writing HTTP response body failed",
					zap.Int("chunk_size", len(chunk)),
					zap.Int("written", n),
					zap.Int64("bytes_written", written),
					zap.Error(err))
				return errors.Wrap(err, "write http response")
			}
			if n != len(chunk) {
				cancel()
				logger.Error("Short write while streaming HTTP response",
					zap.Int("expected", len(chunk)),
					zap.Int("actual", n),
					zap.Int64("bytes_written", written))
				return io.ErrShortWrite
			}

			next++
			if next == len(jobs) {
				err := <-done
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("Telegram media stream failed after completion",
						zap.Int64("bytes_written", written),
						zap.Error(err))
					return errors.Wrap(err, "wait parallel workers")
				}
				logger.Info("Telegram media stream completed",
					zap.Int64("bytes_written", written))
				return nil
			}
		}
	}

	err := <-done
	if err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Warn("Telegram media stream canceled",
				zap.Int64("bytes_written", written),
				zap.Error(err))
			return err
		}
		logger.Error("Telegram media stream failed",
			zap.Int64("bytes_written", written),
			zap.Error(err))
		return errors.Wrap(err, "wait parallel workers")
	}

	if next != len(jobs) {
		return io.ErrUnexpectedEOF
	}

	logger.Info("Telegram media stream completed",
		zap.Int64("bytes_written", written))
	return nil
}

type downloadChunkJob struct {
	index int
	req   telegramChunkRequest
	skip  int
	take  int
}

type downloadChunkResult struct {
	index int
	data  []byte
}

func buildDownloadChunkJobs(start, end int64) []downloadChunkJob {
	if end < start {
		return nil
	}

	remaining := end - start + 1
	next := start
	index := 0
	jobs := make([]downloadChunkJob, 0, int((remaining+int64(telegramGetFileFragmentWindowSize)-1)/int64(telegramGetFileFragmentWindowSize)))
	for next <= end {
		fragmentStart := (next / int64(telegramGetFileFragmentWindowSize)) * int64(telegramGetFileFragmentWindowSize)
		fragmentEnd := fragmentStart + int64(telegramGetFileFragmentWindowSize) - 1
		needStart := next
		needEnd := min(end, fragmentEnd)

		reqOffset := alignDown(needStart, telegramGetFilePreciseAlignment)
		reqEnd := alignUp(needEnd+1, telegramGetFilePreciseAlignment)
		fragmentLimit := fragmentStart + int64(telegramGetFileFragmentWindowSize)
		if reqEnd > fragmentLimit {
			reqEnd = fragmentLimit
		}

		jobs = append(jobs, downloadChunkJob{
			index: index,
			req: telegramChunkRequest{
				offset: reqOffset,
				limit:  int(reqEnd - reqOffset),
			},
			skip: int(needStart - reqOffset),
			take: int(needEnd - needStart + 1),
		})

		next = needEnd + 1
		index++
	}

	return jobs
}

func (s *telegramMediaSource) Media() *tmedia.Media {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.media
}

func (s *telegramMediaSource) FetchChunk(ctx context.Context, pool dcpool.Pool, lease *downloadLease, req telegramChunkRequest) ([]byte, error) {
	for {
		media := s.Media()
		if media == nil {
			return nil, errors.New("telegram media is unavailable")
		}

		if err := lease.AcquireWorker(ctx); err != nil {
			return nil, err
		}
		client := pool.Client(ctx, media.DC)
		data, err := fetchTelegramMediaChunk(ctx, client, media, req)
		lease.ReleaseWorker()
		if err == nil {
			return data, nil
		}
		if !isRefreshableFileReferenceError(err) || s.refresh == nil {
			return nil, err
		}
		if err := s.refreshMedia(ctx, media); err != nil {
			return nil, err
		}
	}
}

func (s *telegramMediaSource) refreshMedia(ctx context.Context, current *tmedia.Media) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	if latest := s.Media(); latest != current {
		return nil
	}

	next, err := s.refresh(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.media = next
	s.mu.Unlock()
	return nil
}

type telegramChunkRequest struct {
	offset int64
	limit  int
}

func fetchTelegramMediaChunk(ctx context.Context, client *tg.Client, media *tmedia.Media, chunkReq telegramChunkRequest) ([]byte, error) {
	logger := logctx.From(ctx)

	for attempt := 0; ; attempt++ {
		req := &tg.UploadGetFileRequest{
			Location: media.InputFileLoc,
			Offset:   chunkReq.offset,
			Limit:    chunkReq.limit,
		}
		req.SetPrecise(true)

		resp, err := client.UploadGetFile(ctx, req)
		if flood, waitErr := tgerr.FloodWait(ctx, err); waitErr != nil {
			if flood || tgerr.Is(waitErr, tg.ErrTimeout) {
				logger.Debug("Retrying telegram file chunk",
					zap.Int64("offset", chunkReq.offset),
					zap.Int("limit", chunkReq.limit),
					zap.Int("attempt", attempt+1),
					zap.Error(waitErr))
				continue
			}
			return nil, errors.Wrap(waitErr, "get telegram file chunk")
		}

		file, ok := resp.(*tg.UploadFile)
		if !ok {
			return nil, fmt.Errorf("unexpected telegram file response %T", resp)
		}
		if len(file.Bytes) == 0 {
			return nil, io.ErrUnexpectedEOF
		}

		return append([]byte(nil), file.Bytes...), nil
	}
}

func sliceTelegramChunk(data []byte, skip, take int) ([]byte, error) {
	if skip < 0 || take < 0 {
		return nil, errors.New("invalid telegram chunk slice")
	}
	end := skip + take
	if end > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	return data[skip:end], nil
}

func alignDown(value int64, alignment int64) int64 {
	if alignment <= 0 {
		return value
	}
	return value - value%alignment
}

func alignUp(value int64, alignment int64) int64 {
	if alignment <= 0 {
		return value
	}
	if value%alignment == 0 {
		return value
	}
	return value + alignment - value%alignment
}

func parseDownloadRange(header string, size int64) (start, end int64, partial bool, err error) {
	if size <= 0 {
		return 0, 0, false, errors.New("invalid content length")
	}
	if header == "" {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, errors.New("invalid range unit")
	}

	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false, errors.New("multiple ranges are not supported")
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range format")
	}

	switch {
	case parts[0] == "":
		suffix, convErr := strconv.ParseInt(parts[1], 10, 64)
		if convErr != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid suffix range")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	case parts[1] == "":
		rangeStart, convErr := strconv.ParseInt(parts[0], 10, 64)
		if convErr != nil || rangeStart < 0 || rangeStart >= size {
			return 0, 0, false, errors.New("invalid range start")
		}
		return rangeStart, size - 1, true, nil
	default:
		rangeStart, startErr := strconv.ParseInt(parts[0], 10, 64)
		rangeEnd, endErr := strconv.ParseInt(parts[1], 10, 64)
		if startErr != nil || endErr != nil || rangeStart < 0 || rangeEnd < rangeStart || rangeStart >= size {
			return 0, 0, false, errors.New("invalid range bounds")
		}
		if rangeEnd >= size {
			rangeEnd = size - 1
		}
		return rangeStart, rangeEnd, true, nil
	}
}
