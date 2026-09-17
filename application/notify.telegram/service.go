package notifytelegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	ID              = "notify.telegram"
	recipientsField = "recipients"
)

func Register(registry *rte.Registry) error {
	minimum, maximum := int64(1), int64(300)
	return registry.Register(manifest.Manifest{
		ID: ID, Title: "Telegram 通知",
		Provides: []manifest.Port{manifest.PortOf[ports.Notifications](ports.NotificationsName, 1, 0)},
		Requires: []manifest.Require{{Port: manifest.PortOf[ports.NotificationTransport](ports.NotificationTransportName, 1, 0), Optional: true}},
		Config: []manifest.ConfigField{
			{Name: recipientsField, Title: "接收者 ID", Type: manifest.Strings, Default: []string{}},
			{Name: "timeout_seconds", Title: "发送超时（秒）", Type: manifest.Int, Default: 15, Min: &minimum, Max: &maximum},
		},
	}, func() rte.Component { return &Service{} })
}

type settings struct {
	recipients []int64
	timeout    time.Duration
}

type Service struct {
	account   types.AccountID
	transport ports.NotificationTransport
	settings  atomic.Pointer[settings]
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.account = k.Account
	s.ctx, s.cancel = context.WithCancel(ctx)
	if transport, err := k.Resolve(ports.NotificationTransportName); err == nil {
		s.transport = transport.(ports.NotificationTransport)
	}
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.NotificationsName, s)
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
	var recipients []string
	var timeout int
	if err := view.Get(recipientsField, &recipients); err != nil {
		return nil, err
	}
	if err := view.Get("timeout_seconds", &timeout); err != nil {
		return nil, err
	}
	next := &settings{timeout: time.Duration(timeout) * time.Second}
	seen := make(map[int64]bool)
	for _, raw := range recipients {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid notification recipient %q", raw)
		}
		if !seen[id] {
			seen[id] = true
			next.recipients = append(next.recipients, id)
		}
	}
	return func() { s.settings.Store(next) }, nil
}

func (s *Service) begin(ctx context.Context, account types.AccountID) (context.Context, func(), *settings, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if account != s.account {
		return nil, nil, nil, errors.New("notification account mismatch")
	}
	if s.closed || s.ctx == nil || s.ctx.Err() != nil {
		return nil, nil, nil, errors.New("notification component is stopped")
	}
	if s.transport == nil {
		return nil, nil, nil, errors.New("notification transport is unavailable")
	}
	settings := s.settings.Load()
	callCtx, cancel := context.WithTimeout(ctx, settings.timeout)
	unlink := context.AfterFunc(s.ctx, cancel)
	s.active.Add(1)
	return callCtx, func() { unlink(); cancel(); s.active.Done() }, settings, nil
}

func (s *Service) Send(ctx context.Context, account types.AccountID, text string) ([]types.NotificationMessage, error) {
	callCtx, done, settings, err := s.begin(ctx, account)
	if err != nil {
		return nil, err
	}
	defer done()
	if text == "" {
		return nil, nil
	}
	var result []types.NotificationMessage
	var combined error
	for _, chatID := range settings.recipients {
		if err := callCtx.Err(); err != nil {
			return result, errors.Join(combined, err)
		}
		id, err := s.transport.Send(callCtx, chatID, text)
		if err != nil {
			combined = errors.Join(combined, fmt.Errorf("notify %d: %w", chatID, err))
			continue
		}
		if id != 0 {
			result = append(result, types.NotificationMessage{Account: account, ChatID: chatID, MessageID: id})
		}
	}
	return result, combined
}

func (s *Service) Edit(ctx context.Context, account types.AccountID, refs []types.NotificationMessage, text string) error {
	callCtx, done, settings, err := s.begin(ctx, account)
	if err != nil {
		return err
	}
	defer done()
	allowed := make(map[int64]bool, len(settings.recipients))
	for _, id := range settings.recipients {
		allowed[id] = true
	}
	// Validate the whole request before editing any messages.
	for _, ref := range refs {
		if ref.Account != account || !allowed[ref.ChatID] || ref.MessageID <= 0 {
			return errors.New("notification message reference is outside the current account recipients")
		}
	}
	if text == "" {
		return nil
	}
	var combined error
	for _, ref := range refs {
		if err := callCtx.Err(); err != nil {
			return errors.Join(combined, err)
		}
		if err := s.transport.Edit(callCtx, ref.ChatID, ref.MessageID, text); err != nil {
			combined = errors.Join(combined, fmt.Errorf("edit notification %d/%d: %w", ref.ChatID, ref.MessageID, err))
		}
	}
	return combined
}
