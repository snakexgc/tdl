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

func (m *Manager) Destinations(ctx context.Context, source types.ChatRef) []types.ForwardDestination {
	value, err := m.componentPort(ports.ForwardRulesName)
	if err != nil {
		return nil
	}
	return value.(ports.ForwardRules).Destinations(ctx, source)
}

type reactionPort struct{ manager *Manager }

func (m *Manager) ConfigurationVersion() uint64 { return m.applyVersion.Load() }

func (p reactionPort) Matches(ctx context.Context, input ports.ReactionInput) bool {
	value, err := p.manager.componentPort(ports.ReactionTriggerName)
	return err == nil && value.(ports.ReactionTrigger).Matches(ctx, input)
}

func (p reactionPort) Claim(ctx context.Context, key ports.ReactionKey) bool {
	value, err := p.manager.componentPort(ports.ReactionTriggerName)
	return err == nil && value.(ports.ReactionTrigger).Claim(ctx, key)
}

func (p reactionPort) Forget(key ports.ReactionKey) {
	if value, err := p.manager.componentPort(ports.ReactionTriggerName); err == nil {
		value.(ports.ReactionTrigger).Forget(key)
	}
}

type messageLinksPort struct{ manager *Manager }

func (p messageLinksPort) Validate(ctx context.Context, account types.AccountID, link string) (string, error) {
	value, err := p.manager.componentPort(ports.MessageLinksName)
	if err != nil {
		return "", err
	}
	return value.(ports.MessageLinks).Validate(ctx, account, link)
}

func (p messageLinksPort) Submit(ctx context.Context, account types.AccountID, link string, source ports.MessageLinkSource, downloads ports.DownloadRequests) (types.DownloadSubmissionSummary, error) {
	value, err := p.manager.componentPort(ports.MessageLinksName)
	if err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	return value.(ports.MessageLinks).Submit(ctx, account, link, source, downloads)
}
