package downloadcontrol

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
)

const ID = "download.control"

func Register(registry *rte.Registry, backends map[string]ports.DownloadBackend) error {
	return registry.Register(Manifest(), func() rte.Component { return New("", backends) })
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.cancel()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.account = k.Account
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	if err := k.Provide(ports.DownloadRoutingName, s); err != nil {
		return err
	}
	return k.Provide(ports.DownloadControlName, s)
}
func (*Service) Start(context.Context) error { return nil }
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
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

func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return nil, nil, fmt.Errorf("download control is stopped")
	}
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	s.active.Add(1)
	return call, func() { unlink(); cancel(); s.active.Done() }, nil
}
