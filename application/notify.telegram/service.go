package notifytelegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/snakexgc/tdl/rte/eventbus"
)

const (
	ID              = "notify.telegram"
	recipientsField = "recipients"
)

func Manifest() manifest.Manifest {
	minimum, maximum := int64(1), int64(300)
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "bot", Title: "机器人与通知", Order: 50, SettingsURL: "/config?tab=bot"},
		ID:      ID, Title: "Telegram 通知",
		Provides:   []manifest.Port{manifest.PortOf[ports.Notifications](ports.NotificationsName, 1, 1)},
		Publishes:  []string{types.NotificationRequested},
		Subscribes: []string{types.NotificationRequested},
		Requires:   []manifest.Require{{Port: manifest.PortOf[ports.NotificationTransport](ports.NotificationTransportName, 1, 0), Optional: true}},
		Config: []manifest.ConfigField{
			manifest.Flag("on_download_start", "下载开始通知", false, false),
			manifest.Flag("on_download_complete", "下载完成通知", false, false),
			manifest.Flag("on_download_pause", "下载暂停通知", false, false),
			manifest.Flag("on_download_error", "下载失败通知", false, false),
			manifest.Flag("live_progress", "实时进度通知", false, false),
			manifest.Number("live_progress_interval_seconds", "进度更新间隔（秒）", 5, 5, 86400, false),

			{Name: recipientsField, Title: "接收者 ID", Type: manifest.Strings, Default: []string{}},
			{Name: "timeout_seconds", Title: "发送超时（秒）", Type: manifest.Int, Default: 15, Min: &minimum, Max: &maximum},
		},
	}, "notifications", "下载通知")
}

func Register(registry *rte.Registry) error {
	return registry.Register(Manifest(), func() rte.Component { return &Service{} })
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
	events    rte.Events
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.account = k.Account
	s.events = k.Events
	s.ctx, s.cancel = context.WithCancel(ctx)
	if transport, err := k.Resolve(ports.NotificationTransportName); err == nil {
		s.transport = transport.(ports.NotificationTransport)
	}
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	if _, err := k.Events.Subscribe(types.NotificationRequested, 64, s.deliverEvent, func(topic string, err error) {
		slog.Error("notification event failed", "component", ID, "account", s.account, "topic", topic, "error", err)
	}); err != nil {
		return err
	}
	return k.Provide(ports.NotificationsName, s)
}

func (s *Service) deliverEvent(ctx context.Context, event eventbus.Event) error {
	var request types.NotificationRequest
	if err := json.Unmarshal(event.Payload, &request); err != nil {
		return err
	}
	_, err := s.Send(ctx, event.Account, request.Text)
	return err
}

func (s *Service) Enqueue(ctx context.Context, account types.AccountID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if account != s.account {
		return errors.New("notification account mismatch")
	}
	if s.closed || s.ctx == nil || s.ctx.Err() != nil {
		return errors.New("notification component is stopped")
	}
	if s.transport == nil {
		return errors.New("notification transport is unavailable")
	}
	if text == "" {
		return nil
	}
	return s.events.Publish(ctx, types.NotificationRequested, types.NotificationRequest{Text: text})
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
	// The batch follows caller and component cancellation. Each transport
	// request gets its own timeout so one recipient cannot exhaust the others'.
	callCtx, cancel := context.WithCancel(ctx)
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
		requestCtx, cancel := context.WithTimeout(callCtx, settings.timeout)
		id, err := s.transport.Send(requestCtx, chatID, text)
		cancel()
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
		requestCtx, cancel := context.WithTimeout(callCtx, settings.timeout)
		err := s.transport.Edit(requestCtx, ref.ChatID, ref.MessageID, text)
		cancel()
		if err != nil {
			combined = errors.Join(combined, fmt.Errorf("edit notification %d/%d: %w", ref.ChatID, ref.MessageID, err))
		}
	}
	return combined
}
