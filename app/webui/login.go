package webui

import (
	"context"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/app/login"
	account "github.com/snakexgc/tdl/application/account.telegram"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

type webLoginManager struct {
	ports.AccountLogin
	opts Options
}

func newWebLoginManager(opts Options) *webLoginManager {
	if opts.Namespace == "" {
		if cfg := config.From(opts.Context); cfg != nil {
			opts.Namespace = cfg.Namespace
		}
	}
	m := &webLoginManager{opts: opts}
	// The account service serializes flows and calls Complete on the same
	// goroutine after Authenticate. SDK state stays within this adapter.
	var authenticatedUser *tg.User
	m.AccountLogin = account.NewLogin(account.LoginOptions{
		Context: opts.Context,
		Prepare: func(raw string) (string, error) {
			namespace, _, err := m.openNamespaceKV(raw)
			return namespace, err
		},
		Authenticate: func(ctx context.Context, namespace, phone string, challenge ports.LoginChallenge) (map[string]any, error) {
			namespace, kvd, err := m.openNamespaceKV(namespace)
			if err != nil {
				return nil, err
			}
			user, err := login.CodeWithAuthenticator(ctx, m.sessionOptions(namespace, kvd), webCodeAuthenticator{LoginChallenge: challenge, phone: phone})
			if err != nil {
				return nil, err
			}
			authenticatedUser = user
			return telegramUserInfo(user), nil
		},
		Complete: func(ctx context.Context, namespace string, user map[string]any) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			restart, err := config.SelectNamespace(ctx, m.currentNamespace(), namespace)
			if err == nil && !restart && opts.OnLoginSuccess != nil {
				opts.OnLoginSuccess(authenticatedUser)
			}
			return restart, err
		},
		RequestReboot: opts.RequestReboot,
	})
	return m
}

func (m *webLoginManager) openNamespaceKV(raw string) (string, storage.Storage, error) {
	namespace, err := config.NormalizeNamespace(raw)
	if err != nil {
		return "", nil, err
	}
	if m.opts.KVEngine != nil {
		kvd, err := m.opts.KVEngine.Open(namespace)
		if err != nil {
			return "", nil, errors.Wrap(err, "open namespace storage")
		}
		return namespace, kvd, nil
	}
	if namespace == m.currentNamespace() && m.opts.NamespaceKV != nil {
		return namespace, m.opts.NamespaceKV, nil
	}
	return "", nil, errors.New("namespace storage is not configured")
}

func (m *webLoginManager) currentNamespace() string {
	if m.opts.Namespace != "" {
		return m.opts.Namespace
	}
	cfg := config.From(m.opts.Context)
	if cfg != nil {
		return cfg.Namespace
	}
	return ""
}

func (m *webLoginManager) sessionOptions(namespace string, kvd storage.Storage) login.SessionOptions {
	cfg := config.From(m.opts.Context)
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	var credentials ports.TelegramCredentials
	if m.opts.Credentials != nil {
		credentials = loginCredentials{source: m.opts.Credentials, owner: types.AccountID(m.currentNamespace()), target: types.AccountID(namespace)}
	}
	return login.SessionOptions{
		Connections: m.opts.Connections, Credentials: credentials, Account: types.AccountID(namespace),
		KV:               kvd,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	}
}

// Credential settings are installation-wide; explicitly bind the current
// profile to the selected login namespace without mislabelling session storage.
type loginCredentials struct {
	source ports.TelegramCredentials
	owner  types.AccountID
	target types.AccountID
}

func (c loginCredentials) Resolve(ctx context.Context, account types.AccountID, preset string) (types.TelegramCredentials, error) {
	if account != c.target {
		return types.TelegramCredentials{}, errors.New("login credential account mismatch")
	}
	owner := c.owner
	if owner == "" {
		owner = types.DefaultAccount
	}
	return c.source.Resolve(ctx, owner, preset)
}

type webCodeAuthenticator struct {
	ports.LoginChallenge
	phone string
}

func (a webCodeAuthenticator) Phone(context.Context) (string, error) { return a.phone, nil }
func (a webCodeAuthenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return a.LoginChallenge.Code(ctx)
}

func (a webCodeAuthenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("sign up is not supported")
}

func (a webCodeAuthenticator) AcceptTermsOfService(_ context.Context, tos tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: tos}
}
