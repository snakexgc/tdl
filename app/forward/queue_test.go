package forward

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/iyear/tdl/core/storage"
)

// memStorage is an in-memory storage.Storage for queue tests.
type memStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStorage() *memStorage { return &memStorage{data: map[string][]byte{}} }

func (m *memStorage) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (m *memStorage) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func newTestQueue() *Queue {
	return &Queue{wake: make(chan struct{}, 1), store: newJobStore(newMemStorage())}
}

func TestQueueEnqueueAndList(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()

	ids, err := q.EnqueueLinks(ctx, []string{"https://t.me/c/1/2", "https://t.me/c/1/3"}, "@dest", "Dest", "clone", false)
	if err != nil {
		t.Fatalf("EnqueueLinks: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 link jobs, got %d", len(ids))
	}
	if _, err := q.EnqueueMessage(ctx, 123, 9, "Origin", "@dest", "Dest", "default", true); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	jobs, err := q.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	for _, job := range jobs {
		if job.Status != StatusQueued {
			t.Fatalf("expected queued, got %q", job.Status)
		}
		if job.Total != 1 {
			t.Fatalf("expected total 1, got %d", job.Total)
		}
	}
}

func TestQueuePauseResumeDelete(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	id, _ := q.EnqueueMessage(ctx, 1, 2, "O", "@d", "D", "default", false)

	if res, _ := q.Pause(ctx, []string{id}); res.Changed != 1 {
		t.Fatalf("expected pause to change 1, got %+v", res)
	}
	if job, _, _ := q.store.Get(ctx, id); job.Status != StatusPaused {
		t.Fatalf("expected paused, got %q", job.Status)
	}
	// Pausing an already-paused job is a no-op.
	if res, _ := q.Pause(ctx, []string{id}); res.Skipped != 1 {
		t.Fatalf("expected pause skip, got %+v", res)
	}
	if res, _ := q.Resume(ctx, []string{id}); res.Changed != 1 {
		t.Fatalf("expected resume to change 1, got %+v", res)
	}
	if job, _, _ := q.store.Get(ctx, id); job.Status != StatusQueued {
		t.Fatalf("expected queued after resume, got %q", job.Status)
	}
	if res, _ := q.Delete(ctx, []string{id}); res.Changed != 1 {
		t.Fatalf("expected delete to change 1, got %+v", res)
	}
	if _, ok, _ := q.store.Get(ctx, id); ok {
		t.Fatal("expected job removed after delete")
	}
}

func TestQueuePauseSkipsRunning(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	job := Job{ID: "fwd-running", Status: StatusRunning, Total: 1}
	if err := q.store.Save(ctx, job); err != nil {
		t.Fatalf("save: %v", err)
	}
	if res, _ := q.Pause(ctx, []string{job.ID}); res.Skipped != 1 || res.Changed != 0 {
		t.Fatalf("expected pause to skip a running job, got %+v", res)
	}
}

func TestQueueResumeRetriesError(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	job := Job{ID: "fwd-err", Status: StatusError, Total: 1, Attempts: 10, Error: "boom"}
	_ = q.store.Save(ctx, job)

	if res, _ := q.Resume(ctx, []string{job.ID}); res.Changed != 1 {
		t.Fatalf("expected resume to re-queue errored job, got %+v", res)
	}
	got, _, _ := q.store.Get(ctx, job.ID)
	if got.Status != StatusQueued || got.Error != "" {
		t.Fatalf("expected queued with cleared error, got status=%q err=%q", got.Status, got.Error)
	}
}

func TestQueuePickNextEligibility(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()

	now := time.Now()
	future := now.Add(time.Hour)
	jobs := []Job{
		{ID: "a", Status: StatusQueued, CreatedAt: now.Add(-3 * time.Minute), Total: 1},
		{ID: "b", Status: StatusPaused, CreatedAt: now.Add(-4 * time.Minute), Total: 1},
		{ID: "c", Status: StatusRetrying, CreatedAt: now.Add(-5 * time.Minute), NextAttemptAt: &future, Total: 1},
		{ID: "d", Status: StatusQueued, CreatedAt: now.Add(-1 * time.Minute), Total: 1},
	}
	for _, j := range jobs {
		if err := q.store.Save(ctx, j); err != nil {
			t.Fatalf("save %s: %v", j.ID, err)
		}
	}

	got, ok := q.pickNext(ctx, q.store)
	if !ok {
		t.Fatal("expected an eligible job")
	}
	// "a" is the oldest among eligible (b is paused, c is backing off).
	if got.ID != "a" {
		t.Fatalf("expected job a first, got %q", got.ID)
	}
}

func TestQueueRecoverRunning(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	started := time.Now()
	job := Job{ID: "fwd-interrupted", Status: StatusRunning, StartedAt: &started, Total: 1}
	_ = q.store.Save(ctx, job)

	q.recoverRunning(ctx, q.store)

	got, _, _ := q.store.Get(ctx, job.ID)
	if got.Status != StatusQueued {
		t.Fatalf("expected interrupted running job re-queued, got %q", got.Status)
	}
	if got.StartedAt != nil {
		t.Fatal("expected started_at cleared on recovery")
	}
}

func TestQueuePruneTerminalTTL(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	old := time.Now().Add(-25 * time.Hour)
	recent := time.Now().Add(-time.Minute)
	stale := Job{ID: "old", Status: StatusDone, CreatedAt: old, FinishedAt: &old, Total: 1}
	fresh := Job{ID: "new", Status: StatusDone, CreatedAt: recent, FinishedAt: &recent, Total: 1}
	_ = q.store.Save(ctx, stale)
	_ = q.store.Save(ctx, fresh)

	jobs, _ := q.store.Records(ctx)
	q.pruneTerminal(ctx, q.store, jobs)

	if _, ok, _ := q.store.Get(ctx, "old"); ok {
		t.Fatal("expected stale finished job pruned")
	}
	if _, ok, _ := q.store.Get(ctx, "new"); !ok {
		t.Fatal("expected recent finished job retained")
	}
}

func TestQueueRunningCount(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	_, _ = q.EnqueueMessage(ctx, 1, 1, "", "", "", "default", false)
	_, _ = q.EnqueueMessage(ctx, 2, 2, "", "", "", "default", false)
	_ = q.store.Save(ctx, Job{ID: "paused", Status: StatusPaused, Total: 1})
	_ = q.store.Save(ctx, Job{ID: "done", Status: StatusDone, Total: 1})

	count, err := q.RunningCount(ctx)
	if err != nil {
		t.Fatalf("RunningCount: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 outstanding (2 queued + 1 paused), got %d", count)
	}
}

func TestBackoffMonotonicAndCapped(t *testing.T) {
	if backoff(1) != backoffBase {
		t.Fatalf("expected first backoff %v, got %v", backoffBase, backoff(1))
	}
	if backoff(2) <= backoff(1) {
		t.Fatal("expected backoff to grow")
	}
	if backoff(100) != backoffMax {
		t.Fatalf("expected capped backoff %v, got %v", backoffMax, backoff(100))
	}
}
