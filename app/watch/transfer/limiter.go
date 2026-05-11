package transfer

import (
	"context"
	"sync"
)

type Limiter struct {
	fileTokens  chan struct{}
	perFileMax  int
	bufferSlots int

	mu    sync.Mutex
	files map[string]*limitState
}

type limitState struct {
	refs          int
	active        bool
	ready         chan struct{}
	requestTokens chan struct{}
	workerTokens  chan struct{}
	bufferTokens  chan struct{}
}

type Lease struct {
	limiter *Limiter
	taskID  string
	state   *limitState
}

func NewLimiter(maxFiles, perFileMax int, bufferSlots ...int) *Limiter {
	maxFiles = normalizeLimit(maxFiles)
	perFileMax = normalizeLimit(perFileMax)
	normalizedBufferSlots := 0
	if len(bufferSlots) > 0 {
		normalizedBufferSlots = normalizeBufferSlots(bufferSlots[0])
	}

	l := &Limiter{
		fileTokens:  make(chan struct{}, maxFiles),
		perFileMax:  perFileMax,
		bufferSlots: normalizedBufferSlots,
		files:       make(map[string]*limitState),
	}
	for i := 0; i < maxFiles; i++ {
		l.fileTokens <- struct{}{}
	}

	return l
}

func normalizeLimit(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func normalizeBufferSlots(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func (l *Limiter) Acquire(ctx context.Context, taskID string) (*Lease, error) {
	state, err := l.acquireFile(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if err := acquireToken(ctx, state.requestTokens); err != nil {
		l.releaseInterest(taskID)
		return nil, err
	}

	return &Lease{
		limiter: l,
		taskID:  taskID,
		state:   state,
	}, nil
}

func (l *Lease) Release() {
	if l == nil {
		return
	}

	releaseToken(l.state.requestTokens)
	l.limiter.releaseInterest(l.taskID)
}

func (l *Lease) AcquireWorker(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return acquireToken(ctx, l.state.workerTokens)
}

func (l *Lease) ReleaseWorker() {
	if l == nil {
		return
	}
	releaseToken(l.state.workerTokens)
}

func (l *Lease) MaxWorkers() int {
	if l == nil {
		return 1
	}
	return cap(l.state.workerTokens)
}

func (l *Lease) BufferSlots() int {
	if l == nil || l.state == nil || l.state.bufferTokens == nil {
		return 0
	}
	return cap(l.state.bufferTokens)
}

func (l *Lease) AcquireBuffer(ctx context.Context) (func(), error) {
	if l == nil || l.state == nil || l.state.bufferTokens == nil {
		return nil, nil
	}
	if err := acquireToken(ctx, l.state.bufferTokens); err != nil {
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			releaseToken(l.state.bufferTokens)
		})
	}, nil
}

func (l *Limiter) acquireFile(ctx context.Context, taskID string) (*limitState, error) {
	registered := false

	for {
		l.mu.Lock()
		state := l.files[taskID]
		if state == nil {
			state = newLimitState(l.perFileMax, l.bufferSlots)
			l.files[taskID] = state
		}
		if !registered {
			state.refs++
			registered = true
		}

		switch {
		case state.active:
			l.mu.Unlock()
			return state, nil
		case state.ready != nil:
			ready := state.ready
			l.mu.Unlock()

			select {
			case <-ctx.Done():
				l.releaseInterest(taskID)
				return nil, ctx.Err()
			case <-ready:
				continue
			}
		default:
			ready := make(chan struct{})
			state.ready = ready
			l.mu.Unlock()

			err := acquireToken(ctx, l.fileTokens)
			l.completeActivation(taskID, ready, err == nil)
			if err != nil {
				l.releaseInterest(taskID)
				return nil, err
			}
		}
	}
}

func (l *Limiter) completeActivation(taskID string, ready chan struct{}, success bool) {
	releaseFileToken := false

	l.mu.Lock()
	state := l.files[taskID]
	switch {
	case state == nil:
		releaseFileToken = success
	case state.ready != ready:
		releaseFileToken = success
	default:
		state.ready = nil
		if success {
			state.active = true
		}
		close(ready)
	}
	l.mu.Unlock()

	if releaseFileToken {
		releaseToken(l.fileTokens)
	}
}

func (l *Limiter) releaseInterest(taskID string) {
	releaseFileToken := false

	l.mu.Lock()
	state := l.files[taskID]
	if state != nil {
		state.refs--
		if state.refs == 0 {
			if state.active {
				state.active = false
				releaseFileToken = true
			}
			if state.ready == nil {
				delete(l.files, taskID)
			}
		}
	}
	l.mu.Unlock()

	if releaseFileToken {
		releaseToken(l.fileTokens)
	}
}

func newLimitState(perFileMax, bufferSlots int) *limitState {
	state := &limitState{
		requestTokens: make(chan struct{}, perFileMax),
		workerTokens:  make(chan struct{}, perFileMax),
	}
	for i := 0; i < perFileMax; i++ {
		state.requestTokens <- struct{}{}
		state.workerTokens <- struct{}{}
	}
	if bufferSlots > 0 {
		state.bufferTokens = make(chan struct{}, bufferSlots)
		for i := 0; i < bufferSlots; i++ {
			state.bufferTokens <- struct{}{}
		}
	}

	return state
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
		panic("download token over-release")
	}
}
