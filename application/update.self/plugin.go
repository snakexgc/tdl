package updater

import (
	"context"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "update.self"

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.Manifest{
		ID: ID, Title: "版本更新",
		Provides: []manifest.Port{manifest.PortOf[ports.Updater](ports.UpdaterName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: "proxy", Title: "代理", Type: manifest.String, Default: "", Secret: true},
		},
	}, func() rte.Component { return &Service{} })
}

type Service struct{ proxy atomic.Pointer[string] }

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.UpdaterName, s)
}
func (*Service) Start(context.Context) error { return nil }
func (*Service) Stop(context.Context) error  { return nil }
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
func (s *Service) Check(ctx context.Context) (Info, error) { return CheckLatest(ctx, *s.proxy.Load()) }
func (s *Service) Download(ctx context.Context) (Plan, Info, error) {
	return DownloadLatest(ctx, *s.proxy.Load())
}
