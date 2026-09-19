package forwarder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type transportFunc func(context.Context, *types.ForwardJob, func(types.ForwardJob)) error

func (f transportFunc) Forward(ctx context.Context, job *types.ForwardJob, report func(types.ForwardJob)) error {
	return f(ctx, job, report)
}

func TestSelectedJobIsRevalidatedBeforeSending(t *testing.T) {
	const pauseAction = "pause"
	for _, action := range []string{pauseAction, "delete"} {
		t.Run(action, func(t *testing.T) {
			q := newTestQueue()
			ctx := context.Background()
			id, _ := q.EnqueueMessage(ctx, 1, 2, "", "", "", forwardModeDefault, false)
			selected, ok := q.pickNext(ctx, q.store)
			if !ok {
				t.Fatal("job not selected")
			}
			if action == pauseAction {
				_, _ = q.Pause(ctx, []string{id})
			} else {
				_, _ = q.Delete(ctx, []string{id})
			}
			q.runJob(ctx, transportFunc(func(context.Context, *types.ForwardJob, func(types.ForwardJob)) error {
				t.Fatal("transport called after control action")
				return nil
			}), q.store, selected)
			job, exists, _ := q.store.Get(ctx, id)
			if action == "delete" && exists {
				t.Fatal("deleted task resurrected")
			}
			if action == pauseAction && job.Status != StatusPaused {
				t.Fatal("paused task overwritten")
			}
		})
	}
}

func TestProgressCannotResurrectDeletedJob(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	id, _ := q.EnqueueMessage(ctx, 1, 2, "", "", "", forwardModeDefault, false)
	job, _, _ := q.store.Get(ctx, id)
	q.runJob(ctx, transportFunc(func(_ context.Context, job *types.ForwardJob, report func(types.ForwardJob)) error {
		_, _ = q.Delete(ctx, []string{id})
		report(*job)
		return nil
	}), q.store, job)
	if _, exists, _ := q.store.Get(ctx, id); exists {
		t.Fatal("deleted job resurrected")
	}
}

func TestTransportFailureRetriesAndManualRetryResetsBudget(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	id, _ := q.EnqueueMessage(ctx, 1, 2, "", "", "", forwardModeDefault, false)
	job, _, _ := q.store.Get(ctx, id)
	fail := transportFunc(func(context.Context, *types.ForwardJob, func(types.ForwardJob)) error {
		return errors.New("send failed")
	})
	q.runJob(ctx, fail, q.store, job)
	job, _, _ = q.store.Get(ctx, id)
	if job.Status != StatusRetrying || job.Attempts != 1 || job.NextAttemptAt == nil {
		t.Fatalf("failure lost: %+v", job)
	}
	job.Attempts = maxAttempts - 1
	_ = q.store.Save(ctx, job)
	q.runJob(ctx, fail, q.store, job)
	job, _, _ = q.store.Get(ctx, id)
	if job.Status != StatusError {
		t.Fatalf("not terminal: %+v", job)
	}
	_, _ = q.Resume(ctx, []string{id})
	job, _, _ = q.store.Get(ctx, id)
	if job.Attempts != 0 || job.FinishedAt != nil {
		t.Fatalf("retry budget not reset: %+v", job)
	}
}

func TestComponentStopWaitsForTransportAndAllowsNewHost(t *testing.T) {
	q := newTestQueue()
	ctx := context.Background()
	id, _ := q.EnqueueMessage(ctx, 1, 2, "", "", "", forwardModeDefault, false)
	entered, release := make(chan struct{}), make(chan struct{})
	registry := rte.NewRegistry()
	err := Register(registry, q, transportFunc(func(ctx context.Context, _ *types.ForwardJob, _ func(types.ForwardJob)) error {
		close(entered)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.Start(ctx)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := host.Stop(deadline); err == nil {
		t.Fatal("stopped before transport drained")
	}
	close(release)
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	job, _, _ := q.store.Get(ctx, id)
	if job.Status != StatusRunning {
		t.Fatalf("interrupted job not recoverable: %+v", job)
	}
	q.recoverRunning(ctx, q.store)
	job, _, _ = q.store.Get(ctx, id)
	if job.Status != StatusQueued {
		t.Fatal("interrupted job not recovered")
	}
}
