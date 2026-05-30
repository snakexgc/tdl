package httpdl

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tmedia"
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
	// LastActiveAt is the sliding expiry clock: the link stays valid until
	// LastActiveAt+TTL. It is refreshed whenever the link is downloaded or while
	// a non-complete aria2/internal task still references it, so an in-progress
	// or queued download cannot have its link expire out from under it. Falls
	// back to CreatedAt when zero (older records, tests).
	LastActiveAt time.Time
}

type persistentDownloadTask struct {
	ID           string                  `json:"id"`
	PeerID       int64                   `json:"peer_id"`
	MessageID    int                     `json:"message_id"`
	Peer         persistentInputPeer     `json:"peer"`
	FileName     string                  `json:"file_name"`
	FileSize     int64                   `json:"file_size"`
	Media        persistentDownloadMedia `json:"media"`
	CreatedAt    time.Time               `json:"created_at"`
	LastActiveAt time.Time               `json:"last_active_at,omitempty"`
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
		CreatedAt:    task.CreatedAt,
		LastActiveAt: task.LastActiveAt,
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
		ID:           p.ID,
		PeerID:       p.PeerID,
		MessageID:    p.MessageID,
		Peer:         peer,
		FileName:     p.FileName,
		FileSize:     p.FileSize,
		Media:        media,
		CreatedAt:    p.CreatedAt,
		LastActiveAt: p.LastActiveAt,
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
			Kind:          mediaKindDocument,
			ID:            loc.ID,
			AccessHash:    loc.AccessHash,
			FileReference: loc.FileReference,
			ThumbSize:     loc.ThumbSize,
		}, nil
	case *tg.InputPhotoFileLocation:
		return persistentMediaLocation{
			Kind:          mediaKindPhoto,
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
	case mediaKindDocument:
		return &tg.InputDocumentFileLocation{
			ID:            p.ID,
			AccessHash:    p.AccessHash,
			FileReference: p.FileReference,
			ThumbSize:     p.ThumbSize,
		}, nil
	case mediaKindPhoto:
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
	ttl   time.Duration
}

func newTaskStore(kv storage.Storage, ttl ...time.Duration) *taskStore {
	taskTTL := defaultDownloadTaskTTL
	if len(ttl) > 0 {
		taskTTL = ttl[0]
	}

	return &taskStore{
		tasks: make(map[string]*downloadTask),
		kv:    kv,
		ttl:   taskTTL,
	}
}

func NewTaskStore(kv storage.Storage, ttl ...time.Duration) *TaskStore {
	return newTaskStore(kv, ttl...)
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
		if err := s.addIndexEntryLocked(ctx, task.ID, downloadTaskExpiryBase(task.CreatedAt, task.LastActiveAt)); err != nil {
			return errors.Wrap(err, "index persistent download task")
		}
	}

	s.tasks[task.ID] = task
	return nil
}

func (s *taskStore) Get(ctx context.Context, id string) (*downloadTask, bool, error) {
	now := time.Now()

	s.mu.RLock()
	task, ok := s.tasks[id]
	s.mu.RUnlock()
	if ok {
		if !isDownloadTaskExpired(downloadTaskExpiryBase(task.CreatedAt, task.LastActiveAt), now, s.ttl) {
			s.touch(ctx, id, nil, now)
			return task, true, nil
		}
		// The cached copy looks expired, but the persisted record may have been
		// refreshed out-of-band (the WebUI activity sync only writes KV, not this
		// in-memory map). The cached LastActiveAt is never newer than KV, so
		// re-check KV before evicting to avoid deleting a still-active link.
		if s.kv != nil {
			refreshed, ok2, err := s.reload(ctx, id, now)
			if err != nil {
				return nil, false, err
			}
			if ok2 {
				return refreshed, true, nil
			}
		}
		if err := s.delete(ctx, id); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	if s.kv == nil {
		return nil, false, nil
	}

	return s.reload(ctx, id, now)
}

// reload loads the persisted task from KV, evicting it when it has truly expired
// (by its own activity clock). On success it caches the parsed task and refreshes
// its activity so an active download keeps the link alive.
func (s *taskStore) reload(ctx context.Context, id string, now time.Time) (*downloadTask, bool, error) {
	data, err := s.kv.Get(ctx, downloadTaskStorageKey(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.mu.Lock()
			delete(s.tasks, id)
			s.mu.Unlock()
			return nil, false, nil
		}
		return nil, false, errors.Wrap(err, "load persistent download task")
	}

	var persisted persistentDownloadTask
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, false, errors.Wrap(err, "decode persistent download task")
	}
	if isDownloadTaskExpired(downloadTaskExpiryBase(persisted.CreatedAt, persisted.LastActiveAt), now, s.ttl) {
		if err := s.delete(ctx, id); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	task, err := persisted.ToTask()
	if err != nil {
		return nil, false, errors.Wrap(err, "restore persistent download task")
	}

	s.mu.Lock()
	s.tasks[id] = task
	s.mu.Unlock()

	s.touch(ctx, id, data, now)
	return task, true, nil
}

// touch slides the link's expiry by stamping LastActiveAt=now on both the record
// and the index. It edits the raw record JSON (rather than re-marshalling the
// in-memory task) so out-of-band fields such as the WebUI "downloaded" flag are
// preserved. The write is throttled to one per refresh interval and is
// best-effort: a failed refresh just means the next request will retry.
func (s *taskStore) touch(ctx context.Context, id string, data []byte, now time.Time) {
	if s == nil || s.kv == nil || s.ttl == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if data == nil {
		var err error
		data, err = s.kv.Get(ctx, downloadTaskStorageKey(id))
		if err != nil {
			return
		}
	}

	updated, changed, err := SetDownloadTaskLastActive(data, now, downloadTaskRefreshInterval(s.ttl))
	if err != nil || !changed {
		return
	}
	if err := s.kv.Set(ctx, downloadTaskStorageKey(id), updated); err != nil {
		return
	}
	if task, ok := s.tasks[id]; ok && task != nil {
		task.LastActiveAt = now
	}
	_ = s.addIndexEntryLocked(ctx, id, now)
}

func (s *taskStore) CleanupExpired(ctx context.Context, now time.Time) error {
	if s.kv == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cleanupExpiredLocked(ctx, now)
}

func (s *taskStore) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.ttl
}

func (s *taskStore) SetTTL(ttl time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ttl = ttl
}

func (s *taskStore) cleanupExpiredLocked(ctx context.Context, now time.Time) error {
	if s.ttl == 0 {
		return nil
	}

	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}

	changed := false
	for id, indexedAt := range index {
		if !isDownloadTaskExpired(indexedAt, now, s.ttl) {
			continue
		}

		// The index timestamp may lag the record (the WebUI activity sync and this
		// store both write it). Confirm against the record's own activity clock
		// before deleting so a refreshed link is never culled.
		base := indexedAt
		data, err := s.kv.Get(ctx, downloadTaskStorageKey(id))
		switch {
		case err == nil:
			var persisted persistentDownloadTask
			if json.Unmarshal(data, &persisted) == nil {
				if recBase := downloadTaskExpiryBase(persisted.CreatedAt, persisted.LastActiveAt); !recBase.IsZero() {
					base = recBase
				}
			}
		case errors.Is(err, storage.ErrNotFound):
			delete(index, id)
			delete(s.tasks, id)
			changed = true
			continue
		default:
			return errors.Wrap(err, "load download task for cleanup")
		}

		if !isDownloadTaskExpired(base, now, s.ttl) {
			if !base.Equal(indexedAt) {
				index[id] = base
				changed = true
			}
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

func isDownloadTaskExpired(createdAt, now time.Time, ttl time.Duration) bool {
	if createdAt.IsZero() || ttl == 0 {
		return false
	}
	return !createdAt.Add(ttl).After(now)
}

// downloadTaskExpiryBase returns the timestamp the TTL is measured from: the
// sliding LastActiveAt when set, otherwise the original CreatedAt.
func downloadTaskExpiryBase(createdAt, lastActiveAt time.Time) time.Time {
	if !lastActiveAt.IsZero() {
		return lastActiveAt
	}
	return createdAt
}

// downloadTaskRefreshInterval is the minimum spacing between activity refreshes,
// bounded so a long-lived link is touched often enough to never lapse mid-download
// while avoiding a KV write on every request.
func downloadTaskRefreshInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 0
	}
	interval := ttl / 4
	if interval > time.Hour {
		interval = time.Hour
	}
	if interval < time.Minute {
		interval = time.Minute
	}
	return interval
}

// RefreshInterval exposes the activity-refresh cadence so out-of-process writers
// (the WebUI activity sync) slide the same clock at the same rate as the proxy.
func RefreshInterval(ttl time.Duration) time.Duration {
	return downloadTaskRefreshInterval(ttl)
}

// SetDownloadTaskLastActive stamps last_active_at=now onto a persisted download
// task record, editing only that field in the raw JSON so every other field
// (media, peer, downloaded flag) is preserved. It is a no-op when the record was
// already refreshed within minInterval, returning changed=false. Shared by the
// download proxy and the WebUI activity sync so both slide one consistent clock.
func SetDownloadTaskLastActive(data []byte, now time.Time, minInterval time.Duration) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, errors.Wrap(err, "decode download task for refresh")
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}

	var last time.Time
	if r, ok := raw["last_active_at"]; ok {
		_ = json.Unmarshal(r, &last)
	}
	if last.IsZero() {
		if r, ok := raw["created_at"]; ok {
			_ = json.Unmarshal(r, &last)
		}
	}
	if minInterval > 0 && !last.IsZero() && now.Sub(last) < minInterval {
		return data, false, nil
	}

	stamp, err := json.Marshal(now)
	if err != nil {
		return nil, false, errors.Wrap(err, "encode last_active_at")
	}
	raw["last_active_at"] = stamp
	updated, err := json.Marshal(raw)
	if err != nil {
		return nil, false, errors.Wrap(err, "encode download task for refresh")
	}
	return updated, true, nil
}
