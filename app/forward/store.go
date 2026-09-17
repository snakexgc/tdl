package forward

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
)

// Forward jobs are persisted to the namespace KV so a serial queue can survive
// process restarts (and resume after network drops). Each job describes one
// logical source message (which may expand to an album) to forward to one
// destination. The worker re-resolves peers and re-fetches the message at run
// time, so nothing session-bound needs to be serialized.

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

type jobStore struct {
	collection *taskhub.Collection
}

func newJobStore(kv storage.Storage) *jobStore {
	s := &jobStore{}
	if kv != nil {
		s.collection = taskhub.Forward(kv)
	}
	return s
}

func (s *jobStore) Save(ctx context.Context, job Job) error {
	if s == nil || s.collection == nil {
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
	data, err := json.Marshal(job)
	if err != nil {
		return errors.Wrap(err, "marshal forward job")
	}
	return s.collection.Put(ctx, job.ID, data, job.CreatedAt)
}

func (s *jobStore) Get(ctx context.Context, id string) (Job, bool, error) {
	if s == nil || s.collection == nil || id == "" {
		return Job{}, false, nil
	}
	data, err := s.collection.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	job, err := decodeJob(id, data)
	return job, err == nil, err
}

func (s *jobStore) Records(ctx context.Context) ([]Job, error) {
	if s == nil || s.collection == nil {
		return nil, nil
	}
	records, err := s.collection.Records(ctx)
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(records))
	for id, data := range records {
		job, err := decodeJob(id, data)
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

func (s *jobStore) Remove(ctx context.Context, id string) error {
	if s == nil || s.collection == nil || id == "" {
		return nil
	}
	return s.collection.Remove(ctx, id)
}

func decodeJob(id string, data []byte) (Job, error) {
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, errors.Wrap(err, "decode forward job")
	}
	if job.ID == "" {
		job.ID = id
	}
	if job.Status == "" {
		job.Status = StatusQueued
	}
	return job, nil
}
