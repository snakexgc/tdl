package transfer

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Scheduler limits the number of active files globally and distributes a
// fixed number of chunk lanes independently within each Telegram DC.
type Scheduler struct {
	fileTokens chan struct{}
	capacity   int

	mu    sync.Mutex
	files map[string]*taskState
	dcs   map[int]*dcState
}

type taskState struct {
	taskID string
	dc     int

	refs       int
	active     bool
	activating chan struct{}
	inFlight   int
	waiters    []*chunkWaiter
}

type dcState struct {
	tasks    []*taskState
	inFlight int
}

type chunkWaiter struct {
	ready    chan struct{}
	granted  bool
	canceled bool
}

// TaskLease keeps a task registered with the global file limiter and its DC
// scheduler. Multiple leases for the same task share one allocation.
type TaskLease struct {
	scheduler *Scheduler
	state     *taskState
	once      sync.Once
}

// ChunkLease represents one scheduled chunk lane. Release is idempotent.
type ChunkLease struct {
	scheduler *Scheduler
	state     *taskState
	once      sync.Once
}

// DCSnapshot is a point-in-time view of one DC scheduler.
type DCSnapshot struct {
	DC             int `json:"dc"`
	Capacity       int `json:"capacity"`
	ActiveChunks   int `json:"active_chunks"`
	ActiveFiles    int `json:"active_files"`
	QueuedFiles    int `json:"queued_files"`
	QueuedRequests int `json:"queued_requests"`
}

func NewScheduler(maxFiles, perDCCapacity int) *Scheduler {
	maxFiles = normalizeLimit(maxFiles)
	perDCCapacity = normalizeLimit(perDCCapacity)
	s := &Scheduler{
		fileTokens: make(chan struct{}, maxFiles),
		capacity:   perDCCapacity,
		files:      make(map[string]*taskState),
		dcs:        make(map[int]*dcState),
	}
	for range maxFiles {
		s.fileTokens <- struct{}{}
	}
	return s
}

func normalizeLimit(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func (s *Scheduler) Capacity() int {
	if s == nil {
		return 1
	}
	return s.capacity
}

func (s *Scheduler) Acquire(ctx context.Context, taskID string, dc int) (*TaskLease, error) {
	if s == nil {
		return nil, fmt.Errorf("download scheduler is not initialized")
	}
	if taskID == "" {
		return nil, fmt.Errorf("download task id is empty")
	}

	state, err := s.acquireTask(ctx, taskID, dc)
	if err != nil {
		return nil, err
	}
	return &TaskLease{scheduler: s, state: state}, nil
}

func (s *Scheduler) acquireTask(ctx context.Context, taskID string, dc int) (*taskState, error) {
	registered := false
	for {
		s.mu.Lock()
		state := s.files[taskID]
		if state == nil {
			state = &taskState{taskID: taskID, dc: dc}
			s.files[taskID] = state
		} else if state.dc != dc {
			s.mu.Unlock()
			if registered {
				s.releaseTask(state)
			}
			return nil, fmt.Errorf("download task %q changed dc from %d to %d", taskID, state.dc, dc)
		}
		if !registered {
			state.refs++
			registered = true
		}

		switch {
		case state.active:
			s.mu.Unlock()
			return state, nil
		case state.activating != nil:
			ready := state.activating
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				s.releaseTask(state)
				return nil, ctx.Err()
			case <-ready:
			}
		default:
			ready := make(chan struct{})
			state.activating = ready
			s.mu.Unlock()

			err := acquireToken(ctx, s.fileTokens)
			s.completeActivation(state, ready, err == nil)
			if err != nil {
				s.releaseTask(state)
				return nil, err
			}
		}
	}
}

func (s *Scheduler) completeActivation(state *taskState, ready chan struct{}, success bool) {
	releaseFile := false
	s.mu.Lock()
	current := s.files[state.taskID]
	switch {
	case current != state:
		releaseFile = success
	case state.activating != ready:
		releaseFile = success
	default:
		state.activating = nil
		if success {
			state.active = true
			dc := s.dcLocked(state.dc)
			dc.tasks = append(dc.tasks, state)
			s.dispatchLocked(state.dc)
		}
		close(ready)
	}
	s.mu.Unlock()
	if releaseFile {
		releaseToken(s.fileTokens)
	}
}

func (l *TaskLease) AcquireChunk(ctx context.Context) (*ChunkLease, error) {
	if l == nil || l.scheduler == nil || l.state == nil {
		return nil, fmt.Errorf("download task lease is not initialized")
	}
	w := &chunkWaiter{ready: make(chan struct{})}
	s := l.scheduler

	s.mu.Lock()
	if !l.state.active || l.state.refs < 1 {
		s.mu.Unlock()
		return nil, fmt.Errorf("download task lease is released")
	}
	l.state.waiters = append(l.state.waiters, w)
	s.dispatchLocked(l.state.dc)
	s.mu.Unlock()

	select {
	case <-w.ready:
		s.mu.Lock()
		granted := w.granted
		s.mu.Unlock()
		if !granted {
			return nil, fmt.Errorf("download task lease is released")
		}
		return &ChunkLease{scheduler: s, state: l.state}, nil
	case <-ctx.Done():
		s.mu.Lock()
		if w.granted {
			s.releaseChunkLocked(l.state)
		} else {
			w.canceled = true
			s.dispatchLocked(l.state.dc)
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (l *TaskLease) Capacity() int {
	if l == nil || l.scheduler == nil {
		return 1
	}
	return l.scheduler.capacity
}

func (l *TaskLease) Release() {
	if l == nil || l.scheduler == nil || l.state == nil {
		return
	}
	l.once.Do(func() { l.scheduler.releaseTask(l.state) })
}

func (l *ChunkLease) Release() {
	if l == nil || l.scheduler == nil || l.state == nil {
		return
	}
	l.once.Do(func() {
		l.scheduler.mu.Lock()
		l.scheduler.releaseChunkLocked(l.state)
		l.scheduler.mu.Unlock()
	})
}

func (s *Scheduler) releaseTask(state *taskState) {
	releaseFile := false
	s.mu.Lock()
	current := s.files[state.taskID]
	if current == state && state.refs > 0 {
		state.refs--
		if state.refs == 0 {
			for _, waiter := range state.waiters {
				if !waiter.canceled {
					waiter.canceled = true
					close(waiter.ready)
				}
			}
			state.waiters = nil
			if state.active && state.inFlight == 0 {
				s.removeActiveTaskLocked(state)
				releaseFile = true
			} else if !state.active && state.activating == nil {
				delete(s.files, state.taskID)
			}
		}
	}
	s.mu.Unlock()
	if releaseFile {
		releaseToken(s.fileTokens)
	}
}

func (s *Scheduler) releaseChunkLocked(state *taskState) {
	dc := s.dcs[state.dc]
	if state.inFlight < 1 || dc == nil || dc.inFlight < 1 {
		panic("download chunk lease over-release")
	}
	state.inFlight--
	dc.inFlight--
	if state.refs == 0 && state.inFlight == 0 {
		s.removeActiveTaskLocked(state)
		releaseToken(s.fileTokens)
		return
	}
	s.dispatchLocked(state.dc)
}

func (s *Scheduler) removeActiveTaskLocked(state *taskState) {
	dc := s.dcs[state.dc]
	if dc != nil {
		for i, candidate := range dc.tasks {
			if candidate == state {
				dc.tasks = append(dc.tasks[:i], dc.tasks[i+1:]...)
				break
			}
		}
	}
	state.active = false
	delete(s.files, state.taskID)
	s.dispatchLocked(state.dc)
}

func (s *Scheduler) dcLocked(dcID int) *dcState {
	dc := s.dcs[dcID]
	if dc == nil {
		dc = &dcState{}
		s.dcs[dcID] = dc
	}
	return dc
}

func (s *Scheduler) dispatchLocked(dcID int) {
	dc := s.dcs[dcID]
	if dc == nil || len(dc.tasks) == 0 {
		return
	}
	admitted := min(s.capacity, len(dc.tasks))
	base := s.capacity / admitted
	remainder := s.capacity % admitted

	for i := 0; i < admitted && dc.inFlight < s.capacity; i++ {
		state := dc.tasks[i]
		quota := base
		if i < remainder {
			quota++
		}
		for state.inFlight < quota && dc.inFlight < s.capacity {
			waiter := popWaiter(state)
			if waiter == nil {
				break
			}
			waiter.granted = true
			state.inFlight++
			dc.inFlight++
			close(waiter.ready)
		}
	}
}

func popWaiter(state *taskState) *chunkWaiter {
	for len(state.waiters) > 0 {
		waiter := state.waiters[0]
		state.waiters = state.waiters[1:]
		if !waiter.canceled {
			return waiter
		}
	}
	return nil
}

func (s *Scheduler) Snapshots() []DCSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]DCSnapshot, 0, len(s.dcs))
	for id, dc := range s.dcs {
		activeFiles := min(s.capacity, len(dc.tasks))
		queuedRequests := 0
		for _, state := range dc.tasks {
			for _, waiter := range state.waiters {
				if !waiter.canceled {
					queuedRequests++
				}
			}
		}
		result = append(result, DCSnapshot{
			DC:             id,
			Capacity:       s.capacity,
			ActiveChunks:   dc.inFlight,
			ActiveFiles:    activeFiles,
			QueuedFiles:    max(0, len(dc.tasks)-activeFiles),
			QueuedRequests: queuedRequests,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DC < result[j].DC })
	return result
}

func acquireToken(ctx context.Context, tokens chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tokens:
		return nil
	}
}

func releaseToken(tokens chan struct{}) {
	select {
	case tokens <- struct{}{}:
	default:
		panic("download file token over-release")
	}
}
