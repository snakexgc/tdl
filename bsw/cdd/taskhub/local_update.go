package taskhub

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

// Create is idempotent across independent handles. Duplicate submissions keep
// the existing task's path, progress and user-selected state.
func (s *LocalRepository) Create(ctx context.Context, record types.LocalDownloadRecord) (types.LocalDownloadRecord, error) {
	return s.create(ctx, record, nil)
}

// CreateLinked checks source identity in the transaction that inserts the task.
// Deletion or replacement during target preparation cannot create an orphan.
func (s *LocalRepository) CreateLinked(ctx context.Context, source ports.ObservedLink, record types.LocalDownloadRecord) (types.LocalDownloadRecord, error) {
	if source.Task.ID == "" || source.Task.ID != record.TaskID || source.Version == "" {
		return types.LocalDownloadRecord{}, errors.New("local source identity is required")
	}
	return s.create(ctx, record, &source)
}

func (s *LocalRepository) create(ctx context.Context, record types.LocalDownloadRecord, source *ports.ObservedLink) (types.LocalDownloadRecord, error) {
	if s == nil || s.kv == nil || record.ID == "" {
		return types.LocalDownloadRecord{}, errors.New("local task storage and id are required")
	}
	c := s.collection()
	if c.initErr != nil {
		return types.LocalDownloadRecord{}, c.initErr
	}
	err := storage.Update(ctx, s.kv, func(tx storage.Storage) error {
		if source != nil {
			data, err := tx.Get(ctx, LinkPrefix+source.Task.ID)
			if errors.Is(err, storage.ErrNotFound) {
				return errors.New("download source was deleted before submission")
			}
			if err != nil {
				return err
			}
			if linkVersion(data) != source.Version {
				return errors.New("download source changed before submission")
			}
		}
		// Keep local writes within their NvM dataset inside the shared transaction.
		tx = Local(tx).store
		data, err := tx.Get(ctx, c.prefix+record.ID)
		if err == nil {
			record, err = decodeLocalRecord(record.ID, data)
			return err
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		if record.TaskID == "" {
			record.TaskID = record.ID
		}
		if record.Status == "" {
			record.Status = types.LocalDownloadStatusQueued
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = time.Now()
		}
		record.UpdatedAt = time.Now()
		record.State = types.NormalizeDownloadState(record.Status)
		record.Revision = 1
		data, err = json.Marshal(record)
		if err != nil {
			return err
		}
		if err = tx.Set(ctx, c.prefix+record.ID, data); err != nil {
			return err
		}
		index[record.ID] = record.CreatedAt
		return c.saveIndex(ctx, tx, index)
	})
	return record, err
}

func (s *LocalRepository) Update(ctx context.Context, id string, change func(*types.LocalDownloadRecord) bool) (bool, error) {
	if s == nil || s.kv == nil {
		return false, errors.New("local task storage is unavailable")
	}
	changed := false
	err := s.collection().Mutate(ctx, id, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		record, err := decodeLocalRecord(id, data)
		if err != nil {
			return nil, stamp, err
		}
		previous, revision := record.State, record.Revision
		if !change(&record) {
			return data, stamp, nil
		}
		record.State = types.NormalizeDownloadState(record.Status)
		if !types.DownloadTransition(previous, record.State) {
			return nil, stamp, errors.New("invalid download state transition")
		}
		record.Revision = revision + 1
		record.ID = id
		record.UpdatedAt = time.Now()
		next, err := json.Marshal(record)
		changed = err == nil
		return next, stamp, err
	})
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return changed && err == nil, err
}

func (s *LocalRepository) MarkDownloaded(ctx context.Context, id string) error {
	if s == nil || s.kv == nil {
		return errors.New("local task storage is unavailable")
	}
	return Links(s.kv).MarkDownloaded(ctx, id)
}
