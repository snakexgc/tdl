package login

import (
	"context"
	"strconv"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
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
	PasswordFunc   func(ctx context.Context) (string, error)
	InputErrorFunc func(ctx context.Context, input string, err error) error
)

const (
	AuthInputCode     = "code"
	AuthInputPassword = "password"

	loginSelfAttempts   = 10
	loginSelfRetryDelay = 500 * time.Millisecond
)

type InputErrorReporter interface {
	AuthInputError(ctx context.Context, input string, err error) error
}

var ErrSessionUnauthorized = errors.New("telegram session is not authorized")

func CodeWithAuthenticator(ctx context.Context, opts SessionOptions, authenticator auth.UserAuthenticator) (*tg.User, error) {
	if authenticator == nil {
		return nil, errors.New("authenticator is nil")
	}

	return runWithTemporarySession(ctx, opts, nil, true, func(ctx context.Context, c *telegram.Client) (*tg.User, error) {
		client := c.Auth()
		status, err := client.Status(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "get auth status")
		}
		if status.Authorized {
			return status.User, nil
		}

		authorization, err := authWithCode(ctx, client, authenticator)
		if err != nil {
			return nil, err
		}
		return authenticatedUserAfterLogin(ctx, c, authorization)
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

func authWithCode(ctx context.Context, client *auth.Client, authenticator auth.UserAuthenticator) (*tg.AuthAuthorization, error) {
	phone, err := authenticator.Phone(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "get phone")
	}

	sentCode, err := client.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "send code")
	}

	switch s := sentCode.(type) {
	case *tg.AuthSentCode:
		return signInWithCode(ctx, client, authenticator, phone, s)
	case *tg.AuthSentCodeSuccess:
		switch authorization := s.Authorization.(type) {
		case *tg.AuthAuthorization:
			return authorization, nil
		case *tg.AuthAuthorizationSignUpRequired:
			return handleSignUp(ctx, client, authenticator, phone, "", &auth.SignUpRequired{
				TermsOfService: authorization.TermsOfService,
			})
		default:
			return nil, errors.Errorf("unexpected authorization type: %T", authorization)
		}
	default:
		return nil, errors.Errorf("unexpected sent code type: %T", sentCode)
	}
}

func signInWithCode(ctx context.Context, client *auth.Client, authenticator auth.UserAuthenticator, phone string, sentCode *tg.AuthSentCode) (*tg.AuthAuthorization, error) {
	for {
		code, err := authenticator.Code(ctx, sentCode)
		if err != nil {
			return nil, errors.Wrap(err, "get code")
		}

		authorization, err := client.SignIn(ctx, phone, code, sentCode.PhoneCodeHash)
		if err == nil {
			return authorization, nil
		}
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			return authPasswordWithRetry(ctx, client, authenticator.Password, reporterInputError(authenticator))
		}
		var signUpRequired *auth.SignUpRequired
		if errors.As(err, &signUpRequired) {
			return handleSignUp(ctx, client, authenticator, phone, sentCode.PhoneCodeHash, signUpRequired)
		}
		if isCodeInputError(err) {
			if reportErr := reportAuthenticatorInputError(ctx, authenticator, AuthInputCode, err); reportErr != nil {
				return nil, errors.Wrap(reportErr, "report code error")
			}
			continue
		}

		return nil, errors.Wrap(err, "sign in")
	}
}

func authPasswordWithRetry(ctx context.Context, client *auth.Client, password PasswordFunc, inputErrors ...InputErrorFunc) (*tg.AuthAuthorization, error) {
	for {
		pwd, err := password(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "2fa password")
		}

		authorization, err := client.Password(ctx, pwd)
		if err == nil {
			return authorization, nil
		}
		if isPasswordInputError(err) {
			if reportErr := reportInputErrorFuncs(ctx, AuthInputPassword, err, inputErrors...); reportErr != nil {
				return nil, errors.Wrap(reportErr, "report password error")
			}
			continue
		}

		return nil, errors.Wrap(err, "2fa auth")
	}
}

func handleSignUp(ctx context.Context, client *auth.Client, authenticator auth.UserAuthenticator, phone, hash string, signUpRequired *auth.SignUpRequired) (*tg.AuthAuthorization, error) {
	if signUpRequired == nil {
		return nil, errors.New("sign up is required")
	}
	if err := authenticator.AcceptTermsOfService(ctx, signUpRequired.TermsOfService); err != nil {
		return nil, errors.Wrap(err, "confirm TOS")
	}
	info, err := authenticator.SignUp(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "sign up info not provided")
	}
	authorization, err := client.SignUp(ctx, auth.SignUp{
		PhoneNumber:   phone,
		PhoneCodeHash: hash,
		FirstName:     info.FirstName,
		LastName:      info.LastName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "sign up")
	}
	return authorization, nil
}

func authenticatedUserAfterLogin(ctx context.Context, c *telegram.Client, authorization *tg.AuthAuthorization) (*tg.User, error) {
	if user, ok := userFromAuthorization(authorization); ok {
		return user, nil
	}

	var last error
	for attempt := 0; attempt < loginSelfAttempts; attempt++ {
		user, err := c.Self(ctx)
		if err == nil {
			if err = validateAuthenticatedUser(user); err == nil {
				return user, nil
			}
			last = err
		} else {
			last = errors.Wrap(err, "get self")
		}

		if attempt+1 < loginSelfAttempts {
			if err := sleepContext(ctx, loginSelfRetryDelay); err != nil {
				return nil, err
			}
		}
	}

	status, err := c.Auth().Status(ctx)
	if err == nil && status.Authorized {
		if err = validateAuthenticatedUser(status.User); err == nil {
			return status.User, nil
		}
		last = err
	} else if err != nil {
		last = errors.Wrap(err, "get auth status")
	}

	if last == nil {
		last = errors.New("authenticated user is nil")
	}
	return nil, last
}

func userFromAuthorization(authorization *tg.AuthAuthorization) (*tg.User, bool) {
	if authorization == nil || authorization.User == nil {
		return nil, false
	}
	user, ok := authorization.User.AsNotEmpty()
	if !ok || validateAuthenticatedUser(user) != nil {
		return nil, false
	}
	return user, true
}

func isCodeInputError(err error) bool {
	return tgerr.Is(err, "PHONE_CODE_EMPTY", "PHONE_CODE_INVALID")
}

func isPasswordInputError(err error) bool {
	return errors.Is(err, auth.ErrPasswordInvalid) || tgerr.Is(err, "PASSWORD_HASH_INVALID")
}

func reporterInputError(authenticator auth.UserAuthenticator) InputErrorFunc {
	return func(ctx context.Context, input string, err error) error {
		return reportAuthenticatorInputError(ctx, authenticator, input, err)
	}
}

func reportAuthenticatorInputError(ctx context.Context, authenticator auth.UserAuthenticator, input string, err error) error {
	reporter, ok := authenticator.(InputErrorReporter)
	if !ok || reporter == nil {
		return nil
	}
	return reporter.AuthInputError(ctx, input, err)
}

func reportInputErrorFuncs(ctx context.Context, input string, err error, inputErrors ...InputErrorFunc) error {
	for _, inputError := range inputErrors {
		if inputError == nil {
			continue
		}
		if reportErr := inputError(ctx, input, err); reportErr != nil {
			return reportErr
		}
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	preflightPing bool,
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
		if preflightPing {
			if err := c.Ping(ctx); err != nil {
				return err
			}
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
