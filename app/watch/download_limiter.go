package watch

import (
	"context"
	"sync"
)

type downloadLimiter struct {
	fileTokens  chan struct{}
	perFileMax  int
	bufferSlots int

	mu    sync.Mutex
	files map[string]*downloadLimitState
}

type downloadLimitState struct {
	refs          int
	active        bool
	ready         chan struct{}
	requestTokens chan struct{}
	workerTokens  chan struct{}
	bufferTokens  chan struct{}
}

type downloadLease struct {
	limiter *downloadLimiter
	taskID  string
	state   *downloadLimitState
}

func newDownloadLimiter(maxFiles, perFileMax int, bufferSlots ...int) *downloadLimiter {
	maxFiles = normalizeDownloadLimit(maxFiles)
	perFileMax = normalizeDownloadLimit(perFileMax)
	normalizedBufferSlots := 0
	if len(bufferSlots) > 0 {
		normalizedBufferSlots = normalizeDownloadBufferSlots(bufferSlots[0])
	}

	l := &downloadLimiter{
		fileTokens:  make(chan struct{}, maxFiles),
		perFileMax:  perFileMax,
		bufferSlots: normalizedBufferSlots,
		files:       make(map[string]*downloadLimitState),
	}
	for i := 0; i < maxFiles; i++ {
		l.fileTokens <- struct{}{}
	}

	return l
}

func normalizeDownloadLimit(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func normalizeDownloadBufferSlots(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func (l *downloadLimiter) Acquire(ctx context.Context, taskID string) (*downloadLease, error) {
	state, err := l.acquireFile(ctx, taskID)
	if err != nil {
		return nil, err
	}

	if err := acquireDownloadToken(ctx, state.requestTokens); err != nil {
		l.releaseInterest(taskID)
		return nil, err
	}

	return &downloadLease{
		limiter: l,
		taskID:  taskID,
		state:   state,
	}, nil
}

func (l *downloadLease) Release() {
	if l == nil {
		return
	}

	releaseDownloadToken(l.state.requestTokens)
	l.limiter.releaseInterest(l.taskID)
}

func (l *downloadLease) AcquireWorker(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return acquireDownloadToken(ctx, l.state.workerTokens)
}

func (l *downloadLease) ReleaseWorker() {
	if l == nil {
		return
	}
	releaseDownloadToken(l.state.workerTokens)
}

func (l *downloadLease) MaxWorkers() int {
	if l == nil {
		return 1
	}
	return cap(l.state.workerTokens)
}

func (l *downloadLease) BufferSlots() int {
	if l == nil || l.state == nil || l.state.bufferTokens == nil {
		return 0
	}
	return cap(l.state.bufferTokens)
}

func (l *downloadLease) AcquireBuffer(ctx context.Context) (func(), error) {
	if l == nil || l.state == nil || l.state.bufferTokens == nil {
		return nil, nil
	}
	if err := acquireDownloadToken(ctx, l.state.bufferTokens); err != nil {
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			releaseDownloadToken(l.state.bufferTokens)
		})
	}, nil
}

func (l *downloadLimiter) acquireFile(ctx context.Context, taskID string) (*downloadLimitState, error) {
	registered := false

	for {
		l.mu.Lock()
		state := l.files[taskID]
		if state == nil {
			state = newDownloadLimitState(l.perFileMax, l.bufferSlots)
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

			err := acquireDownloadToken(ctx, l.fileTokens)
			l.completeActivation(taskID, ready, err == nil)
			if err != nil {
				l.releaseInterest(taskID)
				return nil, err
			}
		}
	}
}

func (l *downloadLimiter) completeActivation(taskID string, ready chan struct{}, success bool) {
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
		releaseDownloadToken(l.fileTokens)
	}
}

func (l *downloadLimiter) releaseInterest(taskID string) {
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
		releaseDownloadToken(l.fileTokens)
	}
}

func newDownloadLimitState(perFileMax, bufferSlots int) *downloadLimitState {
	state := &downloadLimitState{
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

func acquireDownloadToken(ctx context.Context, tokens chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tokens:
		return nil
	}
}

func releaseDownloadToken(tokens chan struct{}) {
	select {
	case tokens <- struct{}{}:
	default:
		panic("download token over-release")
	}
}
