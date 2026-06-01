package forward

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/core/storage"
)

// Forward jobs are persisted to the namespace KV so a serial queue can survive
// process restarts (and resume after network drops). Each job describes one
// logical source message (which may expand to an album) to forward to one
// destination. The worker re-resolves peers and re-fetches the message at run
// time, so nothing session-bound needs to be serialized.

const (
	jobKeyPrefix = "forward.job."
	jobIndexKey  = "forward.index"
)

// Job lifecycle statuses.
const (
	StatusQueued   = "queued"   // eligible to run now
	StatusRunning  = "running"  // currently being forwarded (at most one)
	StatusPaused   = "paused"   // suspended by the user
	StatusRetrying = "retrying" // failed transiently, waiting for backoff
	StatusDone     = "done"     // completed
	StatusError    = "error"    // failed permanently (resume to retry manually)
)

// Job trigger source.
const (
	SourceCommand = "command" // /forward bot command
	SourceWatch   = "watch"   // watcher auto-forward
)

// Job is both the persisted record and the WebUI payload for a forward task.
type Job struct {
	ID     string `json:"id"`
	Source string `json:"source"`

	// Exactly one source form is set: a raw message link (bot command) or a
	// resolved peer+message id (watcher), so the worker can re-fetch it.
	SourceLink      string `json:"source_link,omitempty"`
	SourcePeerID    int64  `json:"source_peer_id,omitempty"`
	SourceMessageID int    `json:"source_message_id,omitempty"`
	OriginName      string `json:"origin_name,omitempty"`

	Destination     string `json:"destination"` // target string ("" = Saved Messages)
	DestinationName string `json:"destination_name,omitempty"`

	Mode   string `json:"mode"` // config mode name: "default" (direct) or "clone"
	Silent bool   `json:"silent,omitempty"`

	Status     string `json:"status"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	CloneDone  int64  `json:"clone_done,omitempty"`
	CloneTotal int64  `json:"clone_total,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	Error      string `json:"error,omitempty"`

	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

func (j Job) terminal() bool {
	return j.Status == StatusDone || j.Status == StatusError
}

type jobIndex map[string]time.Time

type jobStore struct {
	mu sync.Mutex
	kv storage.Storage
}

func newJobStore(kv storage.Storage) *jobStore {
	return &jobStore{kv: kv}
}

func (s *jobStore) Save(ctx context.Context, job Job) error {
	if s == nil || s.kv == nil {
		return errors.New("forward job storage is not configured")
	}
	if strings.TrimSpace(job.ID) == "" {
		return errors.New("forward job id is empty")
	}
	if job.Status == "" {
		job.Status = StatusQueued
	}
	now := time.Now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(job)
	if err != nil {
		return errors.Wrap(err, "marshal forward job")
	}
	if err := s.kv.Set(ctx, jobKeyPrefix+job.ID, data); err != nil {
		return errors.Wrap(err, "persist forward job")
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	index[job.ID] = job.CreatedAt
	return s.saveIndex(ctx, index)
}

func (s *jobStore) Get(ctx context.Context, id string) (Job, bool, error) {
	if s == nil || s.kv == nil || id == "" {
		return Job{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(ctx, id)
}

func (s *jobStore) Records(ctx context.Context) ([]Job, error) {
	if s == nil || s.kv == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	index, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(index))
	changed := false
	for id := range index {
		job, ok, err := s.getLocked(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			delete(index, id)
			changed = true
			continue
		}
		jobs = append(jobs, job)
	}
	if changed {
		if err := s.saveIndex(ctx, index); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func (s *jobStore) Remove(ctx context.Context, id string) error {
	if s == nil || s.kv == nil || id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.kv.Delete(ctx, jobKeyPrefix+id); err != nil {
		return errors.Wrap(err, "delete forward job")
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	delete(index, id)
	return s.saveIndex(ctx, index)
}

func (s *jobStore) getLocked(ctx context.Context, id string) (Job, bool, error) {
	data, err := s.kv.Get(ctx, jobKeyPrefix+id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Job{}, false, nil
		}
		return Job{}, false, errors.Wrap(err, "load forward job")
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, false, errors.Wrap(err, "decode forward job")
	}
	if job.ID == "" {
		job.ID = id
	}
	if job.Status == "" {
		job.Status = StatusQueued
	}
	return job, true, nil
}

func (s *jobStore) loadIndex(ctx context.Context) (jobIndex, error) {
	data, err := s.kv.Get(ctx, jobIndexKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return jobIndex{}, nil
		}
		return nil, errors.Wrap(err, "load forward job index")
	}
	var index jobIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, errors.Wrap(err, "decode forward job index")
	}
	if index == nil {
		index = jobIndex{}
	}
	return index, nil
}

func (s *jobStore) saveIndex(ctx context.Context, index jobIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return errors.Wrap(err, "marshal forward job index")
	}
	if err := s.kv.Set(ctx, jobIndexKey, data); err != nil {
		return errors.Wrap(err, "save forward job index")
	}
	return nil
}
