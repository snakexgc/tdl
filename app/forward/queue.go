package forward

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/forwarder"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
)

const (
	pollInterval    = 2 * time.Second
	persistThrottle = time.Second
	backoffBase     = 5 * time.Second
	backoffMax      = 5 * time.Minute
	maxAttempts     = 10

	// Finished (done/error) jobs are retained for history, then pruned so the KV
	// store stays bounded even under high-volume watcher forwarding.
	terminalTTL = 24 * time.Hour
	maxTerminal = 200
)

// ActionResult mirrors watch.InternalDownloadActionResult so the WebUI can reuse
// the same bulk-action handling.
type ActionResult struct {
	Matched int      `json:"matched"`
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

// Runtime carries the live Telegram client handles the worker needs. It is
// supplied by the watcher (which owns the long-lived authenticated pool) when it
// starts serving the queue.
type Runtime struct {
	Pool    dcpool.Pool
	Manager *peers.Manager
	Threads int
}

// Queue is a process-wide, persistent, single-flight forward task queue. Jobs
// are durably stored so they survive restarts; a single worker drains them one
// at a time and auto-retries transient failures with backoff.
type Queue struct {
	mu    sync.Mutex
	store *jobStore
	wake  chan struct{}

	serving      bool
	activeID     string
	activeCancel context.CancelFunc
	notify       func(context.Context, string)

	seq atomic.Uint64
}

var defaultQueue = &Queue{wake: make(chan struct{}, 1)}

// Jobs returns the process-wide forward queue.
func Jobs() *Queue { return defaultQueue }

// ConfigureQueue binds the queue to the namespace KV store. Call once at daemon
// startup before any module enqueues or serves.
func ConfigureQueue(kv storage.Storage) {
	defaultQueue.mu.Lock()
	defer defaultQueue.mu.Unlock()
	defaultQueue.store = newJobStore(kv)
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

func (q *Queue) jobStore() (*jobStore, error) {
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
	q.signal()
	return ids, nil
}

// EnqueueMessage adds a single job from an already-known source peer/message
// (used by the watcher auto-forward).
func (q *Queue) EnqueueMessage(ctx context.Context, peerID int64, messageID int, originName, target, targetName, mode string, silent bool) (string, error) {
	store, err := q.jobStore()
	if err != nil {
		return "", err
	}
	job := Job{
		ID:              q.nextID(),
		Source:          SourceWatch,
		SourcePeerID:    peerID,
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
		ai, aj := !jobs[i].terminal(), !jobs[j].terminal()
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
		if !job.terminal() {
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
	var result ActionResult
	store, err := q.jobStore()
	if err != nil {
		return result, err
	}
	for _, id := range uniqueIDs(ids) {
		job, ok, err := store.Get(ctx, id)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !ok {
			result.Skipped++
			continue
		}
		if !fn(&job) {
			result.Skipped++
			continue
		}
		if err := store.Save(ctx, job); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
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
func (q *Queue) Serve(ctx context.Context, rt Runtime) error {
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

	if rt.Threads <= 0 {
		rt.Threads = config.DefaultThreads
	}

	q.recoverRunning(ctx, store)

	ticker := time.NewTicker(pollInterval)
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
	}
}

// recoverRunning re-queues jobs left in the running state by a previous process
// that exited mid-forward.
func (q *Queue) recoverRunning(ctx context.Context, store *jobStore) {
	jobs, err := store.Records(ctx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.Status != StatusRunning {
			continue
		}
		job.Status = StatusQueued
		job.StartedAt = nil
		_ = store.Save(ctx, job)
	}
}

// pickNext prunes expired finished jobs and returns the oldest eligible job
// (queued or retrying past its backoff), if any.
func (q *Queue) pickNext(ctx context.Context, store *jobStore) (Job, bool) {
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
func (q *Queue) pruneTerminal(ctx context.Context, store *jobStore, jobs []Job) {
	terminal := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		if job.terminal() {
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
	if len(terminal) > maxTerminal {
		overCap = len(terminal) - maxTerminal
	}
	now := time.Now()
	for idx, job := range terminal {
		drop := idx < overCap || now.Sub(terminalTime(job)) > terminalTTL
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

func (q *Queue) runJob(ctx context.Context, rt Runtime, store *jobStore, job Job) {
	jobCtx, cancel := context.WithCancel(ctx)
	q.setActive(job.ID, cancel)
	defer q.clearActive(job.ID)
	defer cancel()

	now := time.Now()
	job.Status = StatusRunning
	job.StartedAt = &now
	job.Error = ""
	if err := store.Save(jobCtx, job); err != nil {
		return
	}

	runErr := q.forwardJob(jobCtx, rt, &job)

	// Process is shutting down: leave the job running so it is recovered and
	// retried on the next startup.
	if ctx.Err() != nil {
		return
	}
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
		if job.Attempts >= maxAttempts {
			job.Status = StatusError
			job.FinishedAt = &fin
			job.NextAttemptAt = nil
			if notify := q.notifier(); notify != nil {
				text := fmt.Sprintf("转发任务失败（已重试 %d 次）：%s → %s\n错误：%s",
					job.Attempts, forwardJobOrigin(job), forwardJobDestination(job), job.Error)
				go notify(context.WithoutCancel(ctx), text)
			}
		} else {
			next := fin.Add(backoff(job.Attempts))
			job.Status = StatusRetrying
			job.NextAttemptAt = &next
		}
	}
	_ = store.Save(ctx, job)
}

func (q *Queue) forwardJob(ctx context.Context, rt Runtime, job *Job) error {
	to, err := ResolvePeer(ctx, rt.Manager, job.Destination)
	if err != nil {
		return errors.Wrap(err, "resolve destination")
	}
	if strings.TrimSpace(job.DestinationName) == "" {
		job.DestinationName = to.VisibleName()
	}

	from, msgID, err := q.resolveSource(ctx, rt, *job)
	if err != nil {
		return errors.Wrap(err, "resolve source")
	}
	// Prefer the resolved peer's visible name over the placeholder (raw link for
	// bot jobs), keeping the placeholder only if resolution yields no name.
	if name := strings.TrimSpace(from.VisibleName()); name != "" {
		job.OriginName = name
	}

	msg, err := tutil.GetSingleMessage(ctx, rt.Pool.Default(ctx), from.InputPeer(), msgID)
	if err != nil {
		return errors.Wrap(err, "get source message")
	}

	mode, err := NormalizeMode(job.Mode)
	if err != nil {
		return err
	}

	elem := NewElem(from, msg, to, ElemOptions{Mode: mode, Silent: job.Silent, Grouped: true})
	prog := &jobProgress{queue: q, job: job, ctx: ctx}
	fw := forwarder.New(forwarder.Options{
		Pool:     rt.Pool,
		Threads:  rt.Threads,
		Iter:     NewSliceIter([]forwarder.Elem{elem}),
		Progress: prog,
	})
	return fw.Forward(ctx)
}

func (q *Queue) resolveSource(ctx context.Context, rt Runtime, job Job) (peers.Peer, int, error) {
	if strings.TrimSpace(job.SourceLink) != "" {
		return tutil.ParseMessageLink(ctx, rt.Manager, job.SourceLink)
	}
	if job.SourcePeerID == 0 || job.SourceMessageID == 0 {
		return nil, 0, errors.New("forward job has no source message")
	}
	peer, err := tutil.GetInputPeer(ctx, rt.Manager, strconv.FormatInt(job.SourcePeerID, 10))
	if err != nil {
		return nil, 0, err
	}
	return peer, job.SourceMessageID, nil
}

// jobProgress persists live forward progress (throttled) into the job record.
type jobProgress struct {
	queue       *Queue
	job         *Job
	ctx         context.Context
	lastPersist time.Time
}

func (p *jobProgress) OnAdd(elem forwarder.Elem) {
	if from := elem.From(); from != nil && strings.TrimSpace(p.job.OriginName) == "" {
		p.job.OriginName = from.VisibleName()
	}
	if to := elem.To(); to != nil && strings.TrimSpace(p.job.DestinationName) == "" {
		p.job.DestinationName = to.VisibleName()
	}
	p.job.CloneDone = 0
	p.job.CloneTotal = 0
	p.persist(true)
}

func (p *jobProgress) OnClone(_ forwarder.Elem, state forwarder.ProgressState) {
	p.job.CloneDone = state.Done
	p.job.CloneTotal = state.Total
	p.persist(false)
}

func (p *jobProgress) OnDone(_ forwarder.Elem, err error) {
	p.job.Done++
	if err != nil {
		p.job.Error = err.Error()
	}
	p.job.CloneDone = 0
	p.job.CloneTotal = 0
	p.persist(true)
}

func (p *jobProgress) persist(force bool) {
	if p.ctx.Err() != nil {
		return // job canceled or deleted; don't resurrect it
	}
	now := time.Now()
	if !force && now.Sub(p.lastPersist) < persistThrottle {
		return
	}
	p.lastPersist = now
	store, err := p.queue.jobStore()
	if err != nil {
		return
	}
	job := *p.job
	job.Status = StatusRunning
	_ = store.Save(p.ctx, job)
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
