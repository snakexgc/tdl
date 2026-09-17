package watch

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type internalTaskStore struct {
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

	data, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "marshal internal download record")
	}
	return s.collection().Put(ctx, record.ID, data, record.CreatedAt)
}

func (s *internalTaskStore) Get(ctx context.Context, id string) (internalDownloadRecord, bool, error) {
	if s == nil || s.kv == nil || id == "" {
		return internalDownloadRecord{}, false, nil
	}

	return s.get(ctx, id)
}

func (s *internalTaskStore) Records(ctx context.Context) (map[string]internalDownloadRecord, error) {
	result := map[string]internalDownloadRecord{}
	if s == nil || s.kv == nil {
		return result, nil
	}

	records, err := s.collection().Records(ctx)
	if err != nil {
		return nil, err
	}
	for id, data := range records {
		record, err := decodeInternalRecord(id, data)
		if err != nil {
			return nil, err
		}
		result[id] = record
	}

	return result, nil
}

func (s *internalTaskStore) Remove(ctx context.Context, id string) error {
	if s == nil || s.kv == nil || id == "" {
		return nil
	}

	return s.collection().Remove(ctx, id)
}

func (s *internalTaskStore) get(ctx context.Context, id string) (internalDownloadRecord, bool, error) {
	data, err := s.collection().Get(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return internalDownloadRecord{}, false, nil
		}
		return internalDownloadRecord{}, false, errors.Wrap(err, "load internal download record")
	}

	record, err := decodeInternalRecord(id, data)
	return record, err == nil, err
}

func decodeInternalRecord(id string, data []byte) (internalDownloadRecord, error) {
	var record internalDownloadRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return internalDownloadRecord{}, errors.Wrap(err, "decode internal download record")
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
	return record, nil
}

func (s *internalTaskStore) collection() *taskhub.Collection {
	return taskhub.Local(s.kv)
}
