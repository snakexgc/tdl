package runtime

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

func daemonComponentValues(cfg *config.Config) map[string]map[string]any {
	values := watch.PolicyValues(watch.DefaultOptions(cfg))
	values["trigger.reaction"] = map[string]any{"download": append([]string{}, cfg.TriggerReactions...), "forward": append([]string{}, cfg.Forward.TriggerReactions...)}
	values["trigger.messagelink"] = map[string]any{}
	values["account.telegram"] = map[string]any{"api_id": cfg.Telegram.APIID, "api_hash": cfg.Telegram.APIHash, "builtin_preset": cfg.Telegram.BuiltinPreset, "use_builtin": cfg.Telegram.UseBuiltin}
	values["update.self"] = map[string]any{"proxy": config.EffectiveProxy(cfg)}
	return values
}

// The injected facade resolves the current host after recovery from a failed
// initial configuration. Requests never fall back to stale legacy credentials.
func (m *Manager) componentPort(name string) (any, error) {
	m.mu.Lock()
	host := m.policies
	m.mu.Unlock()
	if host == nil {
		return nil, fmt.Errorf("component host is unavailable")
	}
	return host.Resolve(name)
}

func (m *Manager) Resolve(ctx context.Context, account types.AccountID, legacy string) (types.TelegramCredentials, error) {
	value, err := m.componentPort(ports.TelegramCredentialsName)
	if err != nil {
		return types.TelegramCredentials{}, err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	return value.(ports.TelegramCredentials).Resolve(ctx, account, legacy)
}

func (m *Manager) Check(ctx context.Context) (types.UpdateInfo, error) {
	value, err := m.componentPort(ports.UpdaterName)
	if err != nil {
		return types.UpdateInfo{}, err
	}
	return value.(ports.Updater).Check(ctx)
}

func (m *Manager) Download(ctx context.Context) (types.UpdatePlan, types.UpdateInfo, error) {
	value, err := m.componentPort(ports.UpdaterName)
	if err != nil {
		return types.UpdatePlan{}, types.UpdateInfo{}, err
	}
	return value.(ports.Updater).Download(ctx)
}
