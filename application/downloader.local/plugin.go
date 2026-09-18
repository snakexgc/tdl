package local

import (
	"context"
	"sync"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/schedule"
)

const ID = "downloader.local"

func Register(registry *rte.Registry, worker *Worker) error {
	if worker == nil || worker.source == nil || worker.store == nil {
		return errors.New("local downloader requires a source and repository")
	}
	return registry.Register(Manifest(), func() rte.Component {
		return &service{worker: worker}
	})
}

type service struct {
	ctx       context.Context
	worker    *Worker
	account   types.AccountID
	runnables *schedule.Group
	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
}

func (s *service) Init(ctx context.Context, k rte.Kernel) error {
	s.ctx = ctx
	s.account, s.runnables = k.Account, k.Runnables
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.DownloadExecutorName, s)
}

func (s *service) Start(ctx context.Context) error {
	if err := s.worker.Start(ctx); err != nil {
		return err
	}
	return s.runnables.Run("local.downloads", 0, 0, func(ctx context.Context) error {
		<-ctx.Done()
		s.worker.Stop()
		return nil
	}, nil)
}

func (s *service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.active.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.worker.Stop()
	return nil
}

func (*service) Name() string { return "local" }

func (s *service) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadResult{}, err
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return types.DownloadResult{}, errors.New("local downloader is stopped")
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	defer func() { unlink(); cancel() }()
	ctx = call
	if in.Account != s.account {
		return types.DownloadResult{}, errors.New("download account mismatch")
	}
	if in.TaskID == "" || in.FullPath == "" {
		return types.DownloadResult{}, errors.New("task id and target path are required")
	}
	task, ok, err := s.worker.source.Get(ctx, in.TaskID)
	if err != nil {
		return types.DownloadResult{}, err
	}
	if !ok {
		return types.DownloadResult{}, errors.New("source task is unavailable")
	}
	info, err := s.worker.Add(ctx, task, in)
	if err != nil {
		return types.DownloadResult{}, err
	}
	return types.DownloadResult{Account: s.account, Target: s.Name(), ID: info.ID}, nil
}
