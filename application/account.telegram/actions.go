package accounttelegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

type Actions struct {
	account      types.AccountID
	sessions     ports.SessionCatalog
	selection    ports.AccountSelection
	spam         ports.SpamTransport
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed, busy bool
	active       sync.WaitGroup
}

func NewActions(ctx context.Context, account types.AccountID, sessions ports.SessionCatalog, selection ports.AccountSelection, spam ports.SpamTransport) *Actions {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Actions{account: account, sessions: sessions, selection: selection, spam: spam, ctx: ctx, cancel: cancel}
}

func RegisterActions(registry *rte.Registry, actions *Actions) error {
	if actions == nil {
		return errors.New("account actions are required")
	}
	return registry.Register(manifest.Manifest{ID: "account.telegram.actions", Title: "账号操作", Provides: []manifest.Port{manifest.PortOf[ports.AccountActions](ports.AccountActionsName, 1, 0)}}, func() rte.Component { return actions })
}

func (s *Actions) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.AccountActionsName, s)
}
func (*Actions) Start(context.Context) error                    { return nil }
func (*Actions) Reconfigure(context.Context, config.View) error { return nil }
func (s *Actions) Stop(ctx context.Context) error {
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

func (s *Actions) begin(ctx context.Context, account types.AccountID) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if account != s.account {
		return nil, nil, errors.New("account mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return nil, nil, errors.New("account actions are stopped")
	}
	if s.busy {
		return nil, nil, errors.New("account operation already in progress")
	}
	s.busy = true
	s.active.Add(1)
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	return call, func() {
		unlink()
		cancel()
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
		s.active.Done()
	}, nil
}

func (s *Actions) CheckSpam(ctx context.Context, account types.AccountID) (bool, error) {
	ctx, done, err := s.begin(ctx, account)
	if err != nil {
		return false, err
	}
	defer done()
	if s.spam == nil {
		return false, errors.New("spam transport is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	reply, err := s.spam.Reply(ctx, account)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.ToLower(reply), "good news"), nil
}

func (s *Actions) Switch(ctx context.Context, account types.AccountID, target string) (bool, error) {
	ctx, done, err := s.begin(ctx, account)
	if err != nil {
		return false, err
	}
	defer done()
	target = strings.TrimSpace(target)
	if !validSessionName(target) {
		return false, errors.New("invalid session name")
	}
	if s.selection == nil || s.sessions == nil {
		return false, errors.New("account selection is unavailable")
	}
	before, err := s.selection.Current(ctx)
	if err != nil {
		return false, err
	}
	if before == target {
		return false, nil
	}
	sessions, err := s.sessions.List(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.Namespace == target {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if err := s.selection.Select(ctx, before, target); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, errors.New("select an existing authenticated account")
}
