package watch

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/core/storage"
)

const (
	aria2TaskKeyPrefix = "watch.aria2.task."
	aria2TaskIndexKey  = "watch.aria2.index"
)

type aria2TaskRecord struct {
	GID         string    `json:"gid"`
	TaskID      string    `json:"task_id"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	Connections int       `json:"connections"`
	CreatedAt   time.Time `json:"created_at"`
}

type persistentAria2TaskIndex map[string]time.Time

type aria2TaskStore struct {
	mu sync.Mutex
	kv storage.Storage
}

func newAria2TaskStore(kv storage.Storage) *aria2TaskStore {
	return &aria2TaskStore{kv: kv}
}

func (s *aria2TaskStore) Add(ctx context.Context, record aria2TaskRecord) error {
	if s == nil || s.kv == nil {
		return nil
	}
	if record.GID == "" {
		return errors.New("aria2 gid is empty")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.cleanupExpiredLocked(ctx, time.Now()); err != nil {
		return err
	}

	data, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "marshal aria2 task record")
	}
	if err := s.kv.Set(ctx, aria2TaskStorageKey(record.GID), data); err != nil {
		return errors.Wrap(err, "persist aria2 task record")
	}

	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	index[record.GID] = record.CreatedAt
	return s.saveIndex(ctx, index)
}

func (s *aria2TaskStore) GIDs(ctx context.Context) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	if s == nil || s.kv == nil {
		return result, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.cleanupExpiredLocked(ctx, time.Now()); err != nil {
		return nil, err
	}

	index, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	for gid := range index {
		result[gid] = struct{}{}
	}
	return result, nil
}

func (s *aria2TaskStore) Records(ctx context.Context) (map[string]aria2TaskRecord, error) {
	result := map[string]aria2TaskRecord{}
	if s == nil || s.kv == nil {
		return result, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.cleanupExpiredLocked(ctx, time.Now()); err != nil {
		return nil, err
	}

	index, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}

	changed := false
	for gid := range index {
		data, err := s.kv.Get(ctx, aria2TaskStorageKey(gid))
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				delete(index, gid)
				changed = true
				continue
			}
			return nil, errors.Wrap(err, "load aria2 task record")
		}

		var record aria2TaskRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, errors.Wrap(err, "decode aria2 task record")
		}
		if record.GID == "" {
			record.GID = gid
		}
		result[record.GID] = record
	}

	if changed {
		if err := s.saveIndex(ctx, index); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *aria2TaskStore) Remove(ctx context.Context, gid string) error {
	if s == nil || s.kv == nil || gid == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.kv.Delete(ctx, aria2TaskStorageKey(gid)); err != nil {
		return errors.Wrap(err, "delete aria2 task record")
	}

	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	delete(index, gid)
	return s.saveIndex(ctx, index)
}

func (s *aria2TaskStore) cleanupExpiredLocked(ctx context.Context, now time.Time) error {
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}

	changed := false
	for gid, createdAt := range index {
		if !isDownloadTaskExpired(createdAt, now) {
			continue
		}
		if err := s.kv.Delete(ctx, aria2TaskStorageKey(gid)); err != nil {
			return errors.Wrap(err, "delete expired aria2 task record")
		}
		delete(index, gid)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveIndex(ctx, index)
}

func (s *aria2TaskStore) loadIndex(ctx context.Context) (persistentAria2TaskIndex, error) {
	data, err := s.kv.Get(ctx, aria2TaskIndexKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return persistentAria2TaskIndex{}, nil
		}
		return nil, errors.Wrap(err, "load aria2 task index")
	}

	var index persistentAria2TaskIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, errors.Wrap(err, "decode aria2 task index")
	}
	if index == nil {
		index = persistentAria2TaskIndex{}
	}
	return index, nil
}

func (s *aria2TaskStore) saveIndex(ctx context.Context, index persistentAria2TaskIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return errors.Wrap(err, "marshal aria2 task index")
	}
	if err := s.kv.Set(ctx, aria2TaskIndexKey, data); err != nil {
		return errors.Wrap(err, "save aria2 task index")
	}
	return nil
}

func aria2TaskStorageKey(gid string) string {
	return aria2TaskKeyPrefix + gid
}
