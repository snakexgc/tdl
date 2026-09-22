package accounttelegram

import (
	"context"
	"errors"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func RegisterSession(registry *rte.Registry, transport ports.SessionTransport) error {
	if transport == nil {
		return errors.New("session transport is required")
	}
	return registry.Register(manifest.Manifest{
		ID: "account.telegram.session", Title: "账号会话检查",
		Provides: []manifest.Port{manifest.PortOf[ports.AccountSession](ports.AccountSessionName, 1, 0)},
		Requires: []manifest.Require{{Port: manifest.PortOf[ports.AccountResources](ports.AccountResourcesName, 1, 0)}},
	}, func() rte.Component { return &sessionService{transport: transport} })
}

type sessionService struct {
	transport ports.SessionTransport
	account   types.AccountID
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
}

func (s *sessionService) Init(ctx context.Context, k rte.Kernel) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.account = k.Account
	return k.Provide(ports.AccountSessionName, s)
}
func (*sessionService) Start(context.Context) error                    { return nil }
func (*sessionService) Reconfigure(context.Context, config.View) error { return nil }
func (s *sessionService) Stop(ctx context.Context) error {
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

func (s *sessionService) Check(ctx context.Context, account types.AccountID) (*ports.AccountIdentity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if account != s.account {
		return nil, errors.New("session account mismatch")
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, errors.New("session service is stopped")
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	defer func() { unlink(); cancel() }()
	user, err := s.transport.Probe(call)
	if err != nil {
		return nil, err
	}
	if err := call.Err(); err != nil {
		return nil, err
	}
	if user == nil || user.ID == 0 {
		return nil, ports.ErrSessionUnauthorized
	}
	copy := *user
	return &copy, nil
}
