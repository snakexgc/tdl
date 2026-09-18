package forward

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/dcpool"
	"github.com/snakexgc/tdl/internal/core/forwarder"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

const persistThrottle = time.Second

type Runtime struct {
	Pool           dcpool.Pool
	Manager        *peers.Manager
	PoolSize       int
	Account        types.AccountID
	ComponentStore *rteconfig.Store
}
type Queue struct {
	*application.ForwardQueue
	serveMu sync.Mutex
	host    atomic.Pointer[rte.Runtime]
}

func NewQueue(kv storage.Storage) *Queue {
	return &Queue{ForwardQueue: application.NewForwardQueue(newJobStore(kv))}
}

func (q *Queue) Host() *rte.Runtime {
	if q == nil {
		return nil
	}
	return q.host.Load()
}

func (q *Queue) Serve(ctx context.Context, rt Runtime) error {
	if !q.serveMu.TryLock() {
		return errors.New("forward queue is already being served")
	}
	defer q.serveMu.Unlock()
	return application.ServeForwardQueue(ctx, rt.Account, q.ForwardQueue, rt, q.host.Store, rt.ComponentStore)
}

func (rt Runtime) Forward(ctx context.Context, job *Job, report func(Job)) error {
	if rt.PoolSize <= 0 {
		rt.PoolSize = config.DefaultPoolSize
	}
	to, err := ResolvePeer(ctx, rt.Manager, job.Destination)
	if err != nil {
		return errors.Wrap(err, "resolve destination")
	}
	if strings.TrimSpace(job.DestinationName) == "" {
		job.DestinationName = to.VisibleName()
	}

	from, msgID, err := resolveSource(ctx, rt, *job)
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
	prog := &jobProgress{report: report, job: job, ctx: ctx}
	fw := forwarder.New(forwarder.Options{
		Pool:     rt.Pool,
		Threads:  rt.PoolSize,
		Iter:     NewSliceIter([]forwarder.Elem{elem}),
		Progress: prog,
	})
	err = fw.Forward(ctx)
	if err != nil {
		return err
	}
	return prog.failure
}

func resolveSource(ctx context.Context, rt Runtime, job Job) (peers.Peer, int, error) {
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
	report      func(Job)
	failure     error
	mu          sync.Mutex
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
	p.mu.Lock()
	defer p.mu.Unlock()
	p.job.CloneDone = state.Done
	p.job.CloneTotal = state.Total
	p.persist(false)
}

func (p *jobProgress) OnDone(_ forwarder.Elem, err error) {
	p.job.Done++
	if err != nil {
		p.failure = err
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

	job := *p.job
	job.Status = StatusRunning
	p.report(job)
}
