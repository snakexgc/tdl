package updater

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "update.self"

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.Manifest{
		ID: ID, Commands: Commands(), Title: "版本更新",
		Pages:    []manifest.Page{{Path: "/update", Title: "检查更新", View: "update", Module: "/static/js/update.js", Style: "/static/css/update.css", Order: 80}},
		Provides: []manifest.Port{manifest.PortOf[ports.Updater](ports.UpdaterName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: "proxy", Title: "代理", Type: manifest.String, Default: "", Secret: true},
		},
	}, func() rte.Component { return &Service{} })
}

type Service struct {
	proxy  atomic.Pointer[string]
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	active sync.WaitGroup
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.UpdaterName, s)
}
func (*Service) Start(context.Context) error { return nil }
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.active.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (s *Service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var proxy string
	if err := view.Get("proxy", &proxy); err != nil {
		return nil, err
	}
	return func() { s.proxy.Store(&proxy) }, nil
}

func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx == nil || s.ctx.Err() != nil {
		return nil, nil, errors.New("update component is stopped")
	}
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	s.active.Add(1)
	return call, func() { unlink(); cancel(); s.active.Done() }, nil
}

func (s *Service) Check(ctx context.Context) (Info, error) {
	call, done, err := s.begin(ctx)
	if err != nil {
		return Info{}, err
	}
	defer done()
	return CheckLatest(call, *s.proxy.Load())
}

func (s *Service) Download(ctx context.Context) (Plan, Info, error) {
	call, done, err := s.begin(ctx)
	if err != nil {
		return Plan{}, Info{}, err
	}
	defer done()
	return DownloadLatest(call, *s.proxy.Load())
}
