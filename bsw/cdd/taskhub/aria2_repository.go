package taskhub

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const (
	aria2TaskKeyPrefix = Aria2Prefix
	aria2TaskIndexKey  = Aria2Index

	DefaultAria2TaskTTL = 24 * time.Hour
)

type Aria2Record = types.Aria2TaskRecord

type Aria2Repository struct {
	kv  storage.Storage
	ttl time.Duration
}

func NewAria2Repository(kv storage.Storage, ttl ...time.Duration) *Aria2Repository {
	taskTTL := DefaultAria2TaskTTL
	if len(ttl) > 0 {
		taskTTL = ttl[0]
	}
	return &Aria2Repository{kv: kv, ttl: taskTTL}
}

func (s *Aria2Repository) Add(ctx context.Context, record Aria2Record) error {
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
	return Aria2(s.kv).Merge(ctx, record.GID, data, record.CreatedAt)
}

func (s *Aria2Repository) GIDs(ctx context.Context) (map[string]struct{}, error) {
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

func (s *Aria2Repository) Records(ctx context.Context) (map[string]Aria2Record, error) {
	result := make(map[string]Aria2Record)
	if s == nil || s.kv == nil {
		return result, nil
	}
	if err := s.cleanup(ctx); err != nil {
		return nil, err
	}
	records, err := Aria2(s.kv).Records(ctx)
	if err != nil {
		return nil, err
	}
	for gid, data := range records {
		var record Aria2Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		if record.GID == "" {
			record.GID = gid
		}
		record.State = types.NormalizeDownloadState(record.Status)
		result[record.GID] = record
	}
	return result, nil
}

func (s *Aria2Repository) Remove(ctx context.Context, gid string) error {
	if s == nil || s.kv == nil || gid == "" {
		return nil
	}
	return Aria2(s.kv).Remove(ctx, gid)
}

func (s *Aria2Repository) cleanup(ctx context.Context) error {
	if s.ttl <= 0 {
		return nil
	}
	now := time.Now()
	return Aria2(s.kv).Sweep(ctx, func(_ []byte, stamp time.Time) (bool, error) {
		return isAria2TaskExpired(stamp, now, s.ttl), nil
	})
}

func Aria2StorageKey(gid string) string { return aria2TaskKeyPrefix + gid }
func isAria2TaskExpired(createdAt, now time.Time, ttl time.Duration) bool {
	return ttl > 0 && !createdAt.IsZero() && now.Sub(createdAt) > ttl
}
