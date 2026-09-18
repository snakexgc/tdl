package taskhub

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type LocalRepository struct {
	kv storage.Storage
}

func NewLocalRepository(kv storage.Storage) *LocalRepository {
	return &LocalRepository{kv: kv}
}

func (s *LocalRepository) Save(ctx context.Context, record types.LocalDownloadRecord) error {
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
		record.Status = types.InternalDownloadStatusQueued
	}
	now := time.Now()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	record.State = types.NormalizeDownloadState(record.Status)
	if record.Revision == 0 {
		record.Revision = 1
	}

	data, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "marshal internal download record")
	}
	return s.collection().Put(ctx, record.ID, data, record.CreatedAt)
}

func (s *LocalRepository) Get(ctx context.Context, id string) (types.LocalDownloadRecord, bool, error) {
	if s == nil || s.kv == nil || id == "" {
		return types.LocalDownloadRecord{}, false, nil
	}

	return s.get(ctx, id)
}

func (s *LocalRepository) Records(ctx context.Context) (map[string]types.LocalDownloadRecord, error) {
	result := map[string]types.LocalDownloadRecord{}
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

func (s *LocalRepository) Remove(ctx context.Context, id string) error {
	if s == nil || s.kv == nil || id == "" {
		return nil
	}

	return s.collection().Remove(ctx, id)
}

func (s *LocalRepository) get(ctx context.Context, id string) (types.LocalDownloadRecord, bool, error) {
	data, err := s.collection().Get(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return types.LocalDownloadRecord{}, false, nil
		}
		return types.LocalDownloadRecord{}, false, errors.Wrap(err, "load internal download record")
	}

	record, err := decodeInternalRecord(id, data)
	return record, err == nil, err
}

func decodeInternalRecord(id string, data []byte) (types.LocalDownloadRecord, error) {
	var record types.LocalDownloadRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return types.LocalDownloadRecord{}, errors.Wrap(err, "decode internal download record")
	}
	if record.ID == "" {
		record.ID = id
	}
	if record.TaskID == "" {
		record.TaskID = record.ID
	}
	if record.Status == "" {
		record.Status = types.InternalDownloadStatusQueued
	}
	record.State = types.NormalizeDownloadState(record.Status)
	return record, nil
}

func (s *LocalRepository) collection() *Collection {
	return Local(s.kv)
}
