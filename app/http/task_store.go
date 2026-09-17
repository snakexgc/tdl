package httpdl

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tmedia"
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
	if task == nil {
		return errors.New("download task is nil")
	}
	if s.kv == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		copy := *task
		s.tasks[task.ID] = &copy
		return nil
	}
	if err := s.CleanupExpired(ctx, time.Now()); err != nil {
		return err
	}
	persisted, err := persistentDownloadTaskFromTask(task)
	if err != nil {
		return err
	}
	data, err := json.Marshal(persisted)
	if err != nil {
		return err
	}
	return taskhub.Links(s.kv).Merge(ctx, task.ID, data, downloadTaskExpiryBase(task.CreatedAt, task.LastActiveAt))
}

func (s *taskStore) Get(ctx context.Context, id string) (*downloadTask, bool, error) {
	now := time.Now()
	ttl := s.TTL()
	if s.kv == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		task, ok := s.tasks[id]
		if !ok {
			return nil, false, nil
		}
		if isDownloadTaskExpired(downloadTaskExpiryBase(task.CreatedAt, task.LastActiveAt), now, ttl) {
			delete(s.tasks, id)
			return nil, false, nil
		}
		copy := *task
		return &copy, true, nil
	}
	var task *downloadTask
	err := taskhub.Links(s.kv).Mutate(ctx, id, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		var persisted persistentDownloadTask
		if err := json.Unmarshal(data, &persisted); err != nil {
			return nil, stamp, err
		}
		if isDownloadTaskExpired(downloadTaskExpiryBase(persisted.CreatedAt, persisted.LastActiveAt), now, ttl) {
			return nil, stamp, nil
		}
		var err error
		task, err = persisted.ToTask()
		if err != nil {
			return nil, stamp, errors.Wrap(err, "restore persistent download task")
		}
		if ttl > 0 {
			updated, changed, err := SetDownloadTaskLastActive(data, now, downloadTaskRefreshInterval(ttl))
			if err != nil {
				return nil, stamp, err
			}
			if changed {
				data, stamp, task.LastActiveAt = updated, now, now
			}
		}
		return data, stamp, nil
	})
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return task, task != nil, nil
}

func (s *taskStore) CleanupExpired(ctx context.Context, now time.Time) error {
	if s.kv == nil {
		return nil
	}
	ttl := s.TTL()
	if ttl <= 0 {
		return nil
	}
	return taskhub.Links(s.kv).Sweep(ctx, func(data []byte, stamp time.Time) (bool, error) {
		var persisted persistentDownloadTask
		if err := json.Unmarshal(data, &persisted); err != nil {
			return false, err
		}
		if base := downloadTaskExpiryBase(persisted.CreatedAt, persisted.LastActiveAt); !base.IsZero() {
			stamp = base
		}
		return isDownloadTaskExpired(stamp, now, ttl), nil
	})
}

func (s *taskStore) TTL() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
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
