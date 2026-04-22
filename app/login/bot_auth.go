package login

import (
	"context"
	"strconv"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/storage/keygen"
	"github.com/iyear/tdl/pkg/key"
	"github.com/iyear/tdl/pkg/tclient"
)

type SessionOptions struct {
	KV               storage.Storage
	Proxy            string
	NTP              string
	ReconnectTimeout time.Duration
}

type (
	QRShowFunc   func(ctx context.Context, token qrlogin.Token) error
	PasswordFunc func(ctx context.Context) (string, error)
)

var ErrSessionUnauthorized = errors.New("telegram session is not authorized")

func CodeWithAuthenticator(ctx context.Context, opts SessionOptions, authenticator auth.UserAuthenticator) (*tg.User, error) {
	if authenticator == nil {
		return nil, errors.New("authenticator is nil")
	}

	return runWithTemporarySession(ctx, opts, nil, func(ctx context.Context, c *telegram.Client) (*tg.User, error) {
		if err := c.Auth().IfNecessary(ctx, auth.NewFlow(authenticator, auth.SendCodeOptions{})); err != nil {
			return nil, err
		}
		return c.Self(ctx)
	})
}

func CheckSession(ctx context.Context, opts SessionOptions) (*tg.User, error) {
	if opts.KV == nil {
		return nil, errors.New("session storage is nil")
	}

	c, err := tclient.New(ctx, tclient.Options{
		KV:               opts.KV,
		Proxy:            opts.Proxy,
		NTP:              opts.NTP,
		ReconnectTimeout: opts.ReconnectTimeout,
	}, false)
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	var user *tg.User
	if err = c.Run(ctx, func(ctx context.Context) error {
		if err := c.Ping(ctx); err != nil {
			return err
		}

		status, err := c.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			return ErrSessionUnauthorized
		}
		if err := validateAuthenticatedUser(status.User); err != nil {
			return ErrSessionUnauthorized
		}

		user = status.User
		return nil
	}); err != nil {
		return nil, err
	}

	return user, nil
}

func QRWithCallbacks(ctx context.Context, opts SessionOptions, show QRShowFunc, password PasswordFunc) (*tg.User, error) {
	if show == nil {
		return nil, errors.New("qr show callback is nil")
	}
	if password == nil {
		return nil, errors.New("password callback is nil")
	}

	d := tg.NewUpdateDispatcher()
	return runWithTemporarySession(ctx, opts, d, func(ctx context.Context, c *telegram.Client) (*tg.User, error) {
		_, err := c.QR().Auth(ctx, qrlogin.OnLoginToken(d), show)
		if err != nil {
			if !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
				return nil, errors.Wrap(err, "qr auth")
			}

			pwd, err := password(ctx)
			if err != nil {
				return nil, errors.Wrap(err, "2fa password")
			}
			if _, err = c.Auth().Password(ctx, pwd); err != nil {
				return nil, errors.Wrap(err, "2fa auth")
			}
		}

		return c.Self(ctx)
	})
}

func UserSummary(user *tg.User) string {
	if user == nil || user.ID == 0 {
		return "ID: (invalid), Username: (not set), Name: (not set)"
	}

	username := user.Username
	if username == "" {
		username = "(not set)"
	}
	name := user.FirstName
	if user.LastName != "" {
		if name != "" {
			name += " "
		}
		name += user.LastName
	}
	if name == "" {
		name = "(not set)"
	}

	return "ID: " + strconv.FormatInt(user.ID, 10) + ", Username: " + username + ", Name: " + name
}

func runWithTemporarySession(
	ctx context.Context,
	opts SessionOptions,
	updateHandler telegram.UpdateHandler,
	fn func(ctx context.Context, c *telegram.Client) (*tg.User, error),
) (*tg.User, error) {
	if opts.KV == nil {
		return nil, errors.New("session storage is nil")
	}

	tmp := newMemoryStorage()
	if err := tmp.Set(ctx, key.App(), []byte(tclient.AppDesktop)); err != nil {
		return nil, errors.Wrap(err, "set temporary app")
	}

	c, err := tclient.New(ctx, tclient.Options{
		KV:               tmp,
		Proxy:            opts.Proxy,
		NTP:              opts.NTP,
		ReconnectTimeout: opts.ReconnectTimeout,
		UpdateHandler:    updateHandler,
		// The temporary storage starts empty, so loading from it cannot reuse an
		// old real session. It still must be readable during login because gotd
		// can store an auth key and reconnect after PHONE_MIGRATE.
	}, false)
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	var user *tg.User
	if err = c.Run(ctx, func(ctx context.Context) error {
		if err := c.Ping(ctx); err != nil {
			return err
		}

		u, err := fn(ctx, c)
		if err != nil {
			return err
		}
		if err := validateAuthenticatedUser(u); err != nil {
			return err
		}
		user = u

		return commitTemporarySession(ctx, tmp, opts.KV)
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateAuthenticatedUser(user); err != nil {
		return nil, err
	}

	return user, nil
}

func validateAuthenticatedUser(user *tg.User) error {
	if user == nil {
		return errors.New("authenticated user is nil")
	}
	if user.ID == 0 {
		return errors.New("authenticated user has empty id")
	}
	return nil
}

func commitTemporarySession(ctx context.Context, tmp storage.Storage, dst storage.Storage) error {
	session, err := tmp.Get(ctx, keygen.New("session"))
	if err != nil {
		return errors.Wrap(err, "load temporary session")
	}
	if err = dst.Set(ctx, keygen.New("session"), session); err != nil {
		return errors.Wrap(err, "store session")
	}
	if err = dst.Set(ctx, key.App(), []byte(tclient.AppDesktop)); err != nil {
		return errors.Wrap(err, "store app")
	}
	return nil
}
