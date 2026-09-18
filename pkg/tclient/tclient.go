package tclient

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tclient"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/key"
)

type Options struct {
	Connections      *tgauth.Connections
	Credentials      ports.TelegramCredentials
	Account          types.AccountID
	AppOverride      *App
	KV               storage.Storage
	Proxy            string
	NTP              string
	ReconnectTimeout time.Duration
	UpdateHandler    telegram.UpdateHandler
}

func ResolveApp(ctx context.Context, kv storage.Storage) (types.TelegramCredentials, error) {
	return ResolveAppUsing(ctx, kv, "", nil)
}

// ResolveAppUsing uses the production account port when supplied. Standalone
// commands retain legacy configuration until their composition root supplies it.
func ResolveAppUsing(ctx context.Context, kv storage.Storage, account types.AccountID, credentials ports.TelegramCredentials) (types.TelegramCredentials, error) {
	mode, err := kv.Get(ctx, key.App())
	if errors.Is(err, storage.ErrNotFound) {
		mode = []byte(AppBuiltin)
	} else if err != nil {
		return types.TelegramCredentials{}, err
	}
	var settings types.TelegramCredentialsConfig
	if account == "" {
		account = types.DefaultAccount
	}
	if cfg := config.Get(); cfg != nil {
		settings = cfg.Telegram
		if credentials == nil && cfg.Namespace != "" {
			account = types.AccountID(cfg.Namespace)
		}
	}
	if credentials != nil {
		return credentials.Resolve(ctx, account, string(mode))
	}
	return application.ResolveTelegramCredentials(ctx, account, string(mode), settings)
}

func GetApp(ctx context.Context, kv storage.Storage) (App, error) {
	return getAppUsing(ctx, kv, "", nil)
}

func getAppUsing(ctx context.Context, kv storage.Storage, account types.AccountID, credentials ports.TelegramCredentials) (App, error) {
	selected, err := ResolveAppUsing(ctx, kv, account, credentials)
	if err != nil {
		return App{}, err
	}
	// The saved app marker describes the existing session, not a new preset
	// selected in configuration. Read it separately for legacy fingerprinting.
	mode, err := kv.Get(ctx, key.App())
	if errors.Is(err, storage.ErrNotFound) {
		mode = []byte(AppBuiltin)
	} else if err != nil {
		return App{}, err
	}
	legacy, err := application.TelegramPreset(string(mode))
	if err != nil {
		return App{}, err
	}
	if err := tgauth.ValidateCredentials(ctx, kv, selected.App, legacy); err != nil {
		return App{}, err
	}
	return selected.App, nil
}

type Client struct {
	*telegram.Client
	connection *tgauth.Connection
	handler    telegram.UpdateHandler
}

func (c *Client) Run(ctx context.Context, fn func(context.Context) error) error {
	if c.connection != nil {
		return c.connection.Run(ctx, c.handler, fn)
	}
	return c.Client.Run(ctx, fn)
}

func New(ctx context.Context, o Options, login bool, middlewares ...telegram.Middleware) (*Client, error) {
	if o.ReconnectTimeout <= 0 {
		o.ReconnectTimeout = 5 * time.Second
	}
	var app App
	var err error
	if o.AppOverride != nil {
		app = *o.AppOverride
	} else {
		app, err = getAppUsing(ctx, o.KV, o.Account, o.Credentials)
	}
	if err != nil {
		return nil, errors.Wrap(err, "get app")
	}

	sessionStore, err := tgauth.SessionStore(o.KV)
	if err != nil {
		return nil, errors.Wrap(err, "open session dataset")
	}
	factory := func(handler telegram.UpdateHandler) (*telegram.Client, error) {
		lifetime := ctx
		if o.Connections != nil {
			lifetime = o.Connections.Context()
		}
		return tclient.New(lifetime, tclient.Options{
			AppID:            app.AppID,
			AppHash:          app.AppHash,
			Session:          storage.NewSession(sessionStore, login),
			Middlewares:      middlewares,
			Proxy:            o.Proxy,
			NTP:              o.NTP,
			ReconnectTimeout: o.ReconnectTimeout,
			UpdateHandler:    handler,
		})
	}
	if o.Connections != nil {
		key := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%s:%d", app.AppID, app.AppHash, o.Proxy, o.NTP, o.ReconnectTimeout)))
		connection, err := o.Connections.Open(o.Account, fmt.Sprintf("%x", key), factory)
		if err != nil {
			return nil, err
		}
		return &Client{Client: connection.Client, connection: connection, handler: o.UpdateHandler}, nil
	}
	client, err := factory(o.UpdateHandler)
	if err != nil {
		return nil, err
	}
	return &Client{Client: client}, nil
}
