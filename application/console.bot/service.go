package consolebot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	ID           = "console.bot"
	allowedField = "allowed_users"
)

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.Manifest{
		ID: ID, Title: "Bot 控制台",
		Provides: []manifest.Port{manifest.PortOf[ports.Console](ports.ConsoleName, 1, 0)},
		Config:   []manifest.ConfigField{{Name: allowedField, Title: "允许的用户 ID", Type: manifest.Strings, Default: []string{}}},
	}, func() rte.Component { return &Service{} })
}

type Service struct {
	account types.AccountID
	users   atomic.Pointer[map[int64]bool]
	running atomic.Bool
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.account = k.Account
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.ConsoleName, s)
}
func (s *Service) Start(context.Context) error { s.running.Store(true); return nil }
func (s *Service) Stop(context.Context) error  { s.running.Store(false); return nil }
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
	var raw []string
	if err := view.Get(allowedField, &raw); err != nil {
		return nil, err
	}
	next := make(map[int64]bool, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid console user %q", value)
		}
		next[id] = true
	}
	return func() { s.users.Store(&next) }, nil
}

func (s *Service) Allowed(account types.AccountID, user int64) bool {
	if !s.running.Load() || account != s.account {
		return false
	}
	users := s.users.Load()
	return users != nil && (*users)[user]
}
func (*Service) Commands() []types.ConsoleCommand { return commands() }
func (*Service) PrivateCommand(name string) bool  { return privateCommand(name) }
