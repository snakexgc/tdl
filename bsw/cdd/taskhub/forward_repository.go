package taskhub

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type ForwardRepository struct {
	collection *Collection
}

func NewForwardRepository(kv storage.Storage) *ForwardRepository {
	s := &ForwardRepository{}
	if kv != nil {
		s.collection = Forward(kv)
	}
	return s
}

func (s *ForwardRepository) Update(ctx context.Context, id string, change func(*types.ForwardJob) bool) (bool, error) {
	if s == nil || s.collection == nil {
		return false, errors.New("forward job storage is not configured")
	}
	c := s.collection
	if c.initErr != nil {
		return false, c.initErr
	}
	changed := false
	err := storage.Update(ctx, c.store, func(tx storage.Storage) error {
		data, err := tx.Get(ctx, c.prefix+id)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		job, err := decodeForwardJob(id, data)
		if err != nil {
			return err
		}
		if !change(&job) {
			return nil
		}
		job.ID = id
		job.UpdatedAt = time.Now()
		data, err = json.Marshal(job)
		if err != nil {
			return err
		}
		if err := tx.Set(ctx, c.prefix+id, data); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed && err == nil, err
}

func (s *ForwardRepository) Save(ctx context.Context, job types.ForwardJob) error {
	if s == nil || s.collection == nil {
		return errors.New("forward job storage is not configured")
	}
	if strings.TrimSpace(job.ID) == "" {
		return errors.New("forward job id is empty")
	}
	if job.Status == "" {
		job.Status = types.StatusQueued
	}
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	data, err := json.Marshal(job)
	if err != nil {
		return errors.Wrap(err, "marshal forward job")
	}
	return s.collection.Put(ctx, job.ID, data, job.CreatedAt)
}

func (s *ForwardRepository) Get(ctx context.Context, id string) (types.ForwardJob, bool, error) {
	if s == nil || s.collection == nil || id == "" {
		return types.ForwardJob{}, false, nil
	}
	data, err := s.collection.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return types.ForwardJob{}, false, nil
	}
	if err != nil {
		return types.ForwardJob{}, false, err
	}
	job, err := decodeForwardJob(id, data)
	return job, err == nil, err
}

func (s *ForwardRepository) Records(ctx context.Context) ([]types.ForwardJob, error) {
	if s == nil || s.collection == nil {
		return nil, nil
	}
	records, err := s.collection.Records(ctx)
	if err != nil {
		return nil, err
	}
	jobs := make([]types.ForwardJob, 0, len(records))
	for id, data := range records {
		job, err := decodeForwardJob(id, data)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func (s *ForwardRepository) Remove(ctx context.Context, id string) error {
	if s == nil || s.collection == nil || id == "" {
		return nil
	}
	return s.collection.Remove(ctx, id)
}

func decodeForwardJob(id string, data []byte) (types.ForwardJob, error) {
	var job types.ForwardJob
	if err := json.Unmarshal(data, &job); err != nil {
		return types.ForwardJob{}, errors.Wrap(err, "decode forward job")
	}
	if job.ID != id || job.Status == "" {
		return types.ForwardJob{}, errors.New("invalid forward job identity or status")
	}
	return job, nil
}
