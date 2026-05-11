package watch

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/core/storage"
)

const (
	internalTaskKeyPrefix = "watch.internal.task."
	internalTaskIndexKey  = "watch.internal.index"
)

type persistentInternalTaskIndex map[string]time.Time

type internalTaskStore struct {
	mu sync.Mutex
	kv storage.Storage
}

func newInternalTaskStore(kv storage.Storage) *internalTaskStore {
	return &internalTaskStore{kv: kv}
}

func (s *internalTaskStore) Save(ctx context.Context, record internalDownloadRecord) error {
	if s == nil || s.kv == nil {
		return nil
	}
	if strings.TrimSpace(record.ID) == "" {
		return errors.New("internal download id is empty")
	}
	if record.TaskID == "" {
		record.TaskID = record.ID
	}
	if record.Status == "" {
		record.Status = InternalDownloadStatusQueued
	}
	now := time.Now()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "marshal internal download record")
	}
	if err := s.kv.Set(ctx, internalTaskStorageKey(record.ID), data); err != nil {
		return errors.Wrap(err, "persist internal download record")
	}

	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	index[record.ID] = record.CreatedAt
	return s.saveIndex(ctx, index)
}

func (s *internalTaskStore) Get(ctx context.Context, id string) (internalDownloadRecord, bool, error) {
	if s == nil || s.kv == nil || id == "" {
		return internalDownloadRecord{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getLocked(ctx, id)
}

func (s *internalTaskStore) Records(ctx context.Context) (map[string]internalDownloadRecord, error) {
	result := map[string]internalDownloadRecord{}
	if s == nil || s.kv == nil {
		return result, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	index, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	changed := false
	for id := range index {
		record, ok, err := s.getLocked(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			delete(index, id)
			changed = true
			continue
		}
		result[id] = record
	}
	if changed {
		if err := s.saveIndex(ctx, index); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *internalTaskStore) Remove(ctx context.Context, id string) error {
	if s == nil || s.kv == nil || id == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.kv.Delete(ctx, internalTaskStorageKey(id)); err != nil {
		return errors.Wrap(err, "delete internal download record")
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	delete(index, id)
	return s.saveIndex(ctx, index)
}

func (s *internalTaskStore) getLocked(ctx context.Context, id string) (internalDownloadRecord, bool, error) {
	data, err := s.kv.Get(ctx, internalTaskStorageKey(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return internalDownloadRecord{}, false, nil
		}
		return internalDownloadRecord{}, false, errors.Wrap(err, "load internal download record")
	}

	var record internalDownloadRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return internalDownloadRecord{}, false, errors.Wrap(err, "decode internal download record")
	}
	if record.ID == "" {
		record.ID = id
	}
	if record.TaskID == "" {
		record.TaskID = record.ID
	}
	if record.Status == "" {
		record.Status = InternalDownloadStatusQueued
	}
	return record, true, nil
}

func (s *internalTaskStore) loadIndex(ctx context.Context) (persistentInternalTaskIndex, error) {
	data, err := s.kv.Get(ctx, internalTaskIndexKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return persistentInternalTaskIndex{}, nil
		}
		return nil, errors.Wrap(err, "load internal download index")
	}

	var index persistentInternalTaskIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, errors.Wrap(err, "decode internal download index")
	}
	if index == nil {
		index = persistentInternalTaskIndex{}
	}
	return index, nil
}

func (s *internalTaskStore) saveIndex(ctx context.Context, index persistentInternalTaskIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return errors.Wrap(err, "marshal internal download index")
	}
	if err := s.kv.Set(ctx, internalTaskIndexKey, data); err != nil {
		return errors.Wrap(err, "save internal download index")
	}
	return nil
}

func internalTaskStorageKey(id string) string {
	return internalTaskKeyPrefix + id
}
