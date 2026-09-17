package tclient

import (
	"context"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tclient"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/key"
)

type Options struct {
	AppOverride      *App
	KV               storage.Storage
	Proxy            string
	NTP              string
	ReconnectTimeout time.Duration
	UpdateHandler    telegram.UpdateHandler
}

func ResolveApp(ctx context.Context, kv storage.Storage) (types.TelegramCredentials, error) {
	mode, err := kv.Get(ctx, key.App())
	if errors.Is(err, storage.ErrNotFound) {
		mode = []byte(AppBuiltin)
	} else if err != nil {
		return types.TelegramCredentials{}, err
	}
	var settings types.TelegramCredentialsConfig
	account := types.DefaultAccount
	if cfg := config.Get(); cfg != nil {
		settings = cfg.Telegram
		if cfg.Namespace != "" {
			account = types.AccountID(cfg.Namespace)
		}
	}
	return application.ResolveTelegramCredentials(ctx, account, string(mode), settings)
}

func GetApp(ctx context.Context, kv storage.Storage) (App, error) {
	selected, err := ResolveApp(ctx, kv)
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

func New(ctx context.Context, o Options, login bool, middlewares ...telegram.Middleware) (*telegram.Client, error) {
	var app App
	var err error
	if o.AppOverride != nil {
		app = *o.AppOverride
	} else {
		app, err = GetApp(ctx, o.KV)
	}
	if err != nil {
		return nil, errors.Wrap(err, "get app")
	}

	return tclient.New(ctx, tclient.Options{
		AppID:            app.AppID,
		AppHash:          app.AppHash,
		Session:          storage.NewSession(o.KV, login),
		Middlewares:      middlewares,
		Proxy:            o.Proxy,
		NTP:              o.NTP,
		ReconnectTimeout: o.ReconnectTimeout,
		UpdateHandler:    o.UpdateHandler,
	})
}
