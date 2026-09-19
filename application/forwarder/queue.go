package forwarder

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type (
	Job          = types.ForwardJob
	ActionResult = types.ForwardActionResult
)

const (
	StatusQueued   = types.StatusQueued
	StatusRunning  = types.StatusRunning
	StatusPaused   = types.StatusPaused
	StatusRetrying = types.StatusRetrying
	StatusDone     = types.StatusDone
	StatusError    = types.StatusError
	SourceCommand  = types.SourceCommand
	SourceWatch    = types.SourceWatch
)

const (
	pollInterval = 2 * time.Second
	backoffBase  = 5 * time.Second
	backoffMax   = 5 * time.Minute
	maxAttempts  = 10

	// Finished (done/error) jobs are retained for history, then pruned so the KV
	// store stays bounded even under high-volume watcher forwarding.
	terminalTTL = 24 * time.Hour
	maxTerminal = 200
)

// Queue is a namespace-owned, persistent, single-flight forward task queue. Jobs
// are durably stored so they survive restarts; a single worker drains them one
// at a time and auto-retries transient failures with backoff.
type Queue struct {
	dataMu sync.Mutex
	mu     sync.Mutex
	store  ports.ForwardRepository
	wake   chan struct{}

	serving      bool
	activeID     string
	activeCancel context.CancelFunc
	notify       func(context.Context, string)

	seq           atomic.Uint64
	configuration atomic.Pointer[policy]
}

// NewQueue creates a namespace-owned queue. The composition root shares this
// instance with producers, controls and the single worker.
func NewQueue(store ports.ForwardRepository) *Queue {
	return &Queue{wake: make(chan struct{}, 1), store: store}
}

// SetNotifier registers a callback invoked when a job fails permanently (after
// exhausting retries), so the daemon can surface it to the user.
func (q *Queue) SetNotifier(fn func(context.Context, string)) {
	q.mu.Lock()
	q.notify = fn
	q.mu.Unlock()
}

func (q *Queue) notifier() func(context.Context, string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.notify
}

func (q *Queue) jobStore() (ports.ForwardRepository, error) {
	q.mu.Lock()
	store := q.store
	q.mu.Unlock()
	if store == nil {
		return nil, errors.New("forward queue is not configured")
	}
	return store, nil
}

func (q *Queue) nextID() string {
	return fmt.Sprintf("fwd-%d-%d", time.Now().UnixNano(), q.seq.Add(1))
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// EnqueueLinks adds one job per message link (used by the /forward bot command).
func (q *Queue) EnqueueLinks(ctx context.Context, links []string, target, targetName, mode string, silent bool) ([]string, error) {
	store, err := q.jobStore()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(links))
	defer func() {
		if len(ids) > 0 {
			q.signal()
		}
	}()
	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		job := Job{
			ID:              q.nextID(),
			Source:          SourceCommand,
			SourceLink:      link,
			OriginName:      link,
			Destination:     target,
			DestinationName: targetName,
			Mode:            mode,
			Silent:          silent,
			Status:          StatusQueued,
			Total:           1,
		}
		if err := store.Save(ctx, job); err != nil {
			return ids, err
		}
		ids = append(ids, job.ID)
	}
	return ids, nil
}

// EnqueueMessage adds a single job from an already-known source peer/message
// (used by the watcher auto-forward).
func (q *Queue) EnqueueMessage(ctx context.Context, peerID int64, messageID int, originName, target, targetName, mode string, silent bool) (string, error) {
	return q.enqueueMessage(ctx, types.MessagePeer{ID: peerID}, messageID, originName, target, targetName, mode, silent)
}

func (q *Queue) enqueueMessage(ctx context.Context, peer types.MessagePeer, messageID int, originName, target, targetName, mode string, silent bool) (string, error) {
	store, err := q.jobStore()
	if err != nil {
		return "", err
	}
	job := Job{
		ID:              q.nextID(),
		Source:          SourceWatch,
		SourcePeerID:    peer.ID,
		SourcePeerKind:  peer.Kind,
		SourceMessageID: messageID,
		OriginName:      originName,
		Destination:     target,
		DestinationName: targetName,
		Mode:            mode,
		Silent:          silent,
		Status:          StatusQueued,
		Total:           1,
	}
	if err := store.Save(ctx, job); err != nil {
		return "", err
	}
	q.signal()
	return job.ID, nil
}

// EnqueueRouted persists a destination independently. Its stable identity keeps
// a replay or a partial fan-out retry from sending accepted targets twice.
func (q *Queue) EnqueueRouted(ctx context.Context, source types.MessagePeer, messageID int, groupedID int64, origin string, destination types.ForwardDestination) (string, error) {
	q.dataMu.Lock()
	defer q.dataMu.Unlock()
	store, err := q.jobStore()
	if err != nil {
		return "", err
	}
	message := fmt.Sprintf("m:%d", messageID)
	if groupedID != 0 {
		message = fmt.Sprintf("g:%d", groupedID)
	}
	identity := fmt.Sprintf("%s:%d/%s/%s", source.Kind, source.ID, message, destination.Target)
	id := fmt.Sprintf("route-%x", sha256.Sum256([]byte(identity)))
	if _, exists, err := store.Get(ctx, id); err != nil {
		return "", err
	} else if exists {
		return id, nil
	}
	job := Job{
		ID: id, Source: SourceWatch, SourcePeerKind: source.Kind, SourcePeerID: source.ID,
		SourceMessageID: messageID, OriginName: origin, Destination: string(destination.Target),
		RuleID: destination.RuleID, Mode: destination.Mode, Silent: destination.Silent, Status: StatusQueued, Total: 1,
	}
	if err := store.Save(ctx, job); err != nil {
		return "", err
	}
	q.signal()
	return id, nil
}

// List returns all jobs, active (pending/running) first, then most recent.
func (q *Queue) List(ctx context.Context) ([]Job, error) {
	store, err := q.jobStore()
	if err != nil {
		return nil, err
	}
	jobs, err := store.Records(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		ai, aj := !jobs[i].Terminal(), !jobs[j].Terminal()
		if ai != aj {
			return ai
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs, nil
}

// RunningCount reports the number of outstanding jobs (queued, running, paused
// or retrying) — i.e. tasks still in operation.
func (q *Queue) RunningCount(ctx context.Context) (int, error) {
	store, err := q.jobStore()
	if err != nil {
		return 0, err
	}
	jobs, err := store.Records(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, job := range jobs {
		if !job.Terminal() {
			count++
		}
	}
	return count, nil
}

// Pause suspends jobs that are waiting to run. A job already being forwarded
// cannot be interrupted mid-message and is skipped.
func (q *Queue) Pause(ctx context.Context, ids []string) (ActionResult, error) {
	return q.update(ctx, ids, func(job *Job) bool {
		switch job.Status {
		case StatusQueued, StatusRetrying:
			job.Status = StatusPaused
			job.NextAttemptAt = nil
			return true
		default:
			return false
		}
	})
}

// Resume re-queues paused, retrying or errored jobs (doubles as manual retry).
func (q *Queue) Resume(ctx context.Context, ids []string) (ActionResult, error) {
	result, err := q.update(ctx, ids, func(job *Job) bool {
		switch job.Status {
		case StatusPaused, StatusError, StatusRetrying:
			job.Status = StatusQueued
			job.NextAttemptAt = nil
			job.Error = ""
			job.Attempts = 0
			job.FinishedAt = nil
			return true
		default:
			return false
		}
	})
	q.signal()
	return result, err
}

// Delete removes jobs and cancels the one currently being forwarded, if matched.
func (q *Queue) Delete(ctx context.Context, ids []string) (ActionResult, error) {
	q.dataMu.Lock()
	defer q.dataMu.Unlock()
	var result ActionResult
	store, err := q.jobStore()
	if err != nil {
		return result, err
	}
	for _, id := range uniqueIDs(ids) {
		_, ok, err := store.Get(ctx, id)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !ok {
			result.Skipped++
			continue
		}
		if err := store.Remove(ctx, id); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		q.cancelIfActive(id)
		result.Matched++
		result.Changed++
	}
	return result, nil
}

func (q *Queue) update(ctx context.Context, ids []string, fn func(*Job) bool) (ActionResult, error) {
	q.dataMu.Lock()
	defer q.dataMu.Unlock()
	var result ActionResult
	store, err := q.jobStore()
	if err != nil {
		return result, err
	}
	for _, id := range uniqueIDs(ids) {
		changed, err := store.Update(ctx, id, fn)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !changed {
			result.Skipped++
			continue
		}
		result.Matched++
		result.Changed++
	}
	return result, nil
}

func (q *Queue) setActive(id string, cancel context.CancelFunc) {
	q.mu.Lock()
	q.activeID = id
	q.activeCancel = cancel
	q.mu.Unlock()
}

func (q *Queue) clearActive(id string) {
	q.mu.Lock()
	if q.activeID == id {
		q.activeID = ""
		q.activeCancel = nil
	}
	q.mu.Unlock()
}

func (q *Queue) cancelIfActive(id string) {
	q.mu.Lock()
	cancel := q.activeCancel
	active := q.activeID == id
	q.mu.Unlock()
	if active && cancel != nil {
		cancel()
	}
}

// Serve runs the single worker loop, draining the queue one job at a time until
// ctx is canceled. Only one Serve may run at a time.
func (q *Queue) Serve(ctx context.Context, rt ports.ForwardTransport) error {
	store, err := q.jobStore()
	if err != nil {
		return err
	}

	q.mu.Lock()
	if q.serving {
		q.mu.Unlock()
		return errors.New("forward queue is already being served")
	}
	q.serving = true
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		q.serving = false
		q.activeID = ""
		q.activeCancel = nil
		q.mu.Unlock()
	}()

	if rt == nil {
		return errors.New("forward transport is required")
	}

	q.recoverRunning(ctx, store)

	ticker := time.NewTicker(q.policy().poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job, ok := q.pickNext(ctx, store)
		if ok {
			q.runJob(ctx, rt, store, job)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.wake:
		case <-ticker.C:
		}
		ticker.Reset(q.policy().poll)
	}
}

// recoverRunning re-queues jobs left in the running state by a previous process
// that exited mid-forward.
func (q *Queue) recoverRunning(ctx context.Context, store ports.ForwardRepository) {
	q.dataMu.Lock()
	defer q.dataMu.Unlock()
	jobs, err := store.Records(ctx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.Status != StatusRunning {
			continue
		}
		_, _ = store.Update(ctx, job.ID, func(current *Job) bool {
			if current.Status != StatusRunning {
				return false
			}
			current.Status = StatusQueued
			current.StartedAt = nil
			return true
		})
	}
}

// pickNext prunes expired finished jobs and returns the oldest eligible job
// (queued or retrying past its backoff), if any.
func (q *Queue) pickNext(ctx context.Context, store ports.ForwardRepository) (Job, bool) {
	q.dataMu.Lock()
	defer q.dataMu.Unlock()
	jobs, err := store.Records(ctx)
	if err != nil {
		return Job{}, false
	}
	q.pruneTerminal(ctx, store, jobs)

	now := time.Now()
	var best *Job
	for i := range jobs {
		job := jobs[i]
		if job.Status != StatusQueued && job.Status != StatusRetrying {
			continue
		}
		if job.NextAttemptAt != nil && job.NextAttemptAt.After(now) {
			continue
		}
		if best == nil || job.CreatedAt.Before(best.CreatedAt) {
			j := job
			best = &j
		}
	}
	if best == nil {
		return Job{}, false
	}
	return *best, true
}

// pruneTerminal drops finished jobs older than the TTL and caps the number of
// retained finished jobs (oldest first).
func (q *Queue) pruneTerminal(ctx context.Context, store ports.ForwardRepository, jobs []Job) {
	terminal := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		if job.Terminal() {
			terminal = append(terminal, job)
		}
	}
	if len(terminal) == 0 {
		return
	}
	sort.SliceStable(terminal, func(i, j int) bool {
		return terminalTime(terminal[i]).Before(terminalTime(terminal[j]))
	})
	overCap := 0
	settings := q.policy()
	if len(terminal) > settings.history {
		overCap = len(terminal) - settings.history
	}
	now := time.Now()
	for idx, job := range terminal {
		drop := idx < overCap || now.Sub(terminalTime(job)) > settings.retention
		if drop {
			_ = store.Remove(ctx, job.ID)
		}
	}
}

func terminalTime(job Job) time.Time {
	if job.FinishedAt != nil {
		return *job.FinishedAt
	}
	return job.CreatedAt
}

func (q *Queue) runJob(ctx context.Context, rt ports.ForwardTransport, store ports.ForwardRepository, job Job) {
	jobCtx, cancel := context.WithCancel(ctx)
	q.setActive(job.ID, cancel)
	defer q.clearActive(job.ID)
	defer cancel()

	q.dataMu.Lock()
	claimed, err := store.Update(jobCtx, job.ID, func(current *Job) bool {
		if current.Status != StatusQueued && current.Status != StatusRetrying {
			return false
		}
		now := time.Now()
		current.Status = StatusRunning
		current.StartedAt = &now
		current.Error = ""
		job = *current
		return true
	})
	q.dataMu.Unlock()
	if err != nil || !claimed {
		return
	}

	runErr := rt.Forward(jobCtx, &job, func(progress Job) {
		q.dataMu.Lock()
		defer q.dataMu.Unlock()
		if jobCtx.Err() != nil {
			return
		}
		_, _ = store.Update(jobCtx, progress.ID, func(current *Job) bool {
			if current.Status != StatusRunning {
				return false
			}
			progress.Status = StatusRunning
			*current = progress
			return true
		})
	})

	// Process is shutting down: leave the job running so it is recovered and
	// retried on the next startup.
	if ctx.Err() != nil {
		return
	}
	var notification string
	q.dataMu.Lock()
	defer func() {
		q.dataMu.Unlock()
		if notification != "" {
			if notify := q.notifier(); notify != nil {
				notify(ctx, notification)
			}
		}
	}()
	// Deleted while running: do not resurrect it.
	if _, ok, err := store.Get(ctx, job.ID); err != nil || !ok {
		return
	}

	fin := time.Now()
	switch {
	case runErr == nil:
		job.Status = StatusDone
		job.Done = job.Total
		job.Error = ""
		job.FinishedAt = &fin
		job.NextAttemptAt = nil
	case errors.Is(runErr, context.Canceled):
		// Canceled but neither shutdown nor deleted: re-queue for a fresh attempt.
		job.Status = StatusQueued
		job.StartedAt = nil
	default:
		job.Attempts++
		job.Error = runErr.Error()
		job.StartedAt = nil
		if job.Attempts >= q.policy().attempts {
			job.Status = StatusError
			job.FinishedAt = &fin
			job.NextAttemptAt = nil
			notification = fmt.Sprintf("转发任务失败（已重试 %d 次）：%s → %s\n错误：%s",
				job.Attempts, forwardJobOrigin(job), forwardJobDestination(job), job.Error)
		} else {
			next := fin.Add(q.policy().backoff(job.Attempts))
			job.Status = StatusRetrying
			job.NextAttemptAt = &next
		}
	}
	changed, err := store.Update(ctx, job.ID, func(current *Job) bool {
		if current.Status != StatusRunning {
			return false
		}
		*current = job
		return true
	})
	if err != nil || !changed {
		notification = ""
	}
}

func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := backoffBase << (attempts - 1)
	if d <= 0 || d > backoffMax {
		return backoffMax
	}
	return d
}

func forwardJobOrigin(job Job) string {
	if s := strings.TrimSpace(job.OriginName); s != "" {
		return s
	}
	if s := strings.TrimSpace(job.SourceLink); s != "" {
		return s
	}
	if job.SourcePeerID != 0 {
		return fmt.Sprintf("ID %d", job.SourcePeerID)
	}
	return "未知来源"
}

func forwardJobDestination(job Job) string {
	if s := strings.TrimSpace(job.DestinationName); s != "" {
		return s
	}
	if s := strings.TrimSpace(job.Destination); s != "" {
		return s
	}
	return "收藏夹"
}

func uniqueIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
