package tclient

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"

	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tclient"
)

type Options struct {
	Connections      *tgauth.Connections
	Credentials      ports.TelegramCredentials
	Account          types.AccountID
	AppOverride      *types.TelegramApp
	KV               storage.Storage
	Proxy            string
	ReconnectTimeout time.Duration
	UpdateHandler    telegram.UpdateHandler
}

func ResolveApp(ctx context.Context, account types.AccountID, credentials ports.TelegramCredentials) (types.TelegramCredentials, error) {
	if err := ctx.Err(); err != nil {
		return types.TelegramCredentials{}, err
	}
	if credentials == nil {
		return types.TelegramCredentials{}, errors.New("account credential service is unavailable")
	}
	return credentials.Resolve(ctx, account)
}

func getAppUsing(ctx context.Context, kv storage.Storage, account types.AccountID, credentials ports.TelegramCredentials) (types.TelegramApp, error) {
	selected, err := ResolveApp(ctx, account, credentials)
	if err != nil {
		return types.TelegramApp{}, err
	}
	if err := tgauth.ValidateCredentials(ctx, kv, selected.App); err != nil {
		return types.TelegramApp{}, err
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
		return nil, errors.New("reconnect timeout must be positive")
	}
	var app types.TelegramApp
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
			ReconnectTimeout: o.ReconnectTimeout,
			UpdateHandler:    handler,
		})
	}
	if o.Connections != nil {
		key := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%d", app.AppID, app.AppHash, o.Proxy, o.ReconnectTimeout)))
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
