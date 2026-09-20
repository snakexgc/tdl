package updater

import (
	"context"
	"errors"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "update.self"

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "update", Title: "软件更新", Order: 80, SettingsURL: "/config?tab=network"},
		ID:      ID, Commands: Commands(), Title: "版本更新",
		Pages:    []manifest.Page{{Path: "/update", Title: "检查更新", View: "update", Module: "/static/js/update.js", Style: "/static/css/update.css", Order: 80, KeepVisible: true, SettingsURL: "/modules#feature-update"}},
		Provides: []manifest.Port{manifest.PortOf[ports.Updater](ports.UpdaterName, 1, 0)},
		// Disabling the configuration provider must not reject the whole policy
		// host. Init fails this consumer only when the shared proxy is unavailable.
		Requires: []manifest.Require{{Port: manifest.PortOf[ports.NetworkProxy](ports.NetworkProxyName, 1, 0), Optional: true}},
		Config:   []manifest.ConfigField{},
	}, "network", "网络代理"), func() rte.Component { return &Service{} })
}

type Service struct {
	proxy  ports.NetworkProxy
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	active sync.WaitGroup
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	value, err := k.Resolve(ports.NetworkProxyName)
	if err != nil {
		return err
	}
	s.proxy = value.(ports.NetworkProxy)
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
	return func() {}, nil
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
	proxy, err := s.proxy.Proxy(call)
	if err != nil {
		return Info{}, err
	}
	return CheckLatest(call, proxy)
}

func (s *Service) Download(ctx context.Context) (Plan, Info, error) {
	call, done, err := s.begin(ctx)
	if err != nil {
		return Plan{}, Info{}, err
	}
	defer done()
	proxy, err := s.proxy.Proxy(call)
	if err != nil {
		return Plan{}, Info{}, err
	}
	return DownloadLatest(call, proxy)
}
