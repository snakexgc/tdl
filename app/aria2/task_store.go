package aria2

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const (
	aria2TaskKeyPrefix = taskhub.Aria2Prefix
	aria2TaskIndexKey  = taskhub.Aria2Index

	DefaultTaskTTL = 24 * time.Hour
)

type TaskRecord struct {
	GID         string    `json:"gid"`
	TaskID      string    `json:"task_id"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`
	Total       int64     `json:"total"`
	Completed   int64     `json:"completed"`
	Error       string    `json:"error,omitempty"`
}

type aria2TaskRecord = TaskRecord

type TaskStore struct {
	kv  storage.Storage
	ttl time.Duration
}

func NewTaskStore(kv storage.Storage, ttl ...time.Duration) *TaskStore {
	taskTTL := DefaultTaskTTL
	if len(ttl) > 0 {
		taskTTL = ttl[0]
	}
	return &TaskStore{kv: kv, ttl: taskTTL}
}

func (s *TaskStore) Add(ctx context.Context, record TaskRecord) error {
	if s == nil || s.kv == nil {
		return nil
	}
	if record.GID == "" {
		return errors.New("aria2 gid is empty")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if err := s.cleanup(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return taskhub.Aria2(s.kv).Merge(ctx, record.GID, data, record.CreatedAt)
}

func (s *TaskStore) GIDs(ctx context.Context) (map[string]struct{}, error) {
	records, err := s.Records(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(records))
	for gid := range records {
		result[gid] = struct{}{}
	}
	return result, nil
}

func (s *TaskStore) Records(ctx context.Context) (map[string]TaskRecord, error) {
	result := make(map[string]TaskRecord)
	if s == nil || s.kv == nil {
		return result, nil
	}
	if err := s.cleanup(ctx); err != nil {
		return nil, err
	}
	records, err := taskhub.Aria2(s.kv).Records(ctx)
	if err != nil {
		return nil, err
	}
	for gid, data := range records {
		var record TaskRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		if record.GID == "" {
			record.GID = gid
		}
		result[record.GID] = record
	}
	return result, nil
}

func (s *TaskStore) Remove(ctx context.Context, gid string) error {
	if s == nil || s.kv == nil || gid == "" {
		return nil
	}
	return taskhub.Aria2(s.kv).Remove(ctx, gid)
}

func (s *TaskStore) cleanup(ctx context.Context) error {
	if s.ttl <= 0 {
		return nil
	}
	now := time.Now()
	return taskhub.Aria2(s.kv).Sweep(ctx, func(_ []byte, stamp time.Time) (bool, error) {
		return isTaskExpired(stamp, now, s.ttl), nil
	})
}

func StorageKey(gid string) string { return aria2TaskKeyPrefix + gid }
func isTaskExpired(createdAt, now time.Time, ttl time.Duration) bool {
	return ttl > 0 && !createdAt.IsZero() && now.Sub(createdAt) > ttl
}
