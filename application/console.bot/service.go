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

func Manifest() manifest.Manifest {
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "bot", Title: "机器人与通知", Order: 50, SettingsURL: "/config?tab=bot"},
		ID:      ID, Commands: Commands(), Title: "Bot 控制台",
		Provides: []manifest.Port{manifest.PortOf[ports.Console](ports.ConsoleName, 1, 0)},
		Config:   []manifest.ConfigField{manifest.Text("token", "机器人 Token", "", true, true), {Name: "proxy", Title: "旧版机器人代理（已停用）", Type: manifest.String, Default: "", Secret: true, ReplacedBy: "account.telegram.proxy", Help: "仅兼容读取旧配置；实际使用网络配置中的统一网络代理。"}, {Name: allowedField, Title: "允许的用户 ID", Type: manifest.Strings, Default: []string{}}},
	}, "bot", "机器人访问")
}

func Register(registry *rte.Registry) error { return RegisterCommands(registry, Commands()) }
func RegisterCommands(registry *rte.Registry, commands []types.ConsoleCommand) error {
	return registry.Register(Manifest(), func() rte.Component {
		s := &Service{}
		s.SetCommands(commands)
		return s
	})
}

type Service struct {
	commands atomic.Pointer[[]types.ConsoleCommand]
	account  types.AccountID
	users    atomic.Pointer[map[int64]bool]
	running  atomic.Bool
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

func (s *Service) Commands() []types.ConsoleCommand {
	commands := s.commands.Load()
	if commands == nil {
		return cloneCommands(Commands())
	}
	return cloneCommands(*commands)
}

// SetCommands publishes the composition root's complete enabled command set.
// Readers never observe a partially updated menu or retain an old owner list.
func (s *Service) SetCommands(commands []types.ConsoleCommand) {
	copy := cloneCommands(commands)
	s.commands.Store(&copy)
}

func (s *Service) PrivateCommand(name string) bool {
	for _, command := range s.Commands() {
		if command.Name == name {
			return true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return true
			}
		}
	}
	return false
}
