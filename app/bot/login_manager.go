package bot

import (
	"context"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/snakexgc/tdl/app/login"
	account "github.com/snakexgc/tdl/application/account.telegram"
	"github.com/snakexgc/tdl/interfaces/ports"
)

var errLoginBusy = ports.ErrLoginBusy

type botAPI interface {
	SendMessage(context.Context, *telego.SendMessageParams) (*telego.Message, error)
	DeleteMessage(context.Context, *telego.DeleteMessageParams) error
}
type loginRunner interface {
	LoginCode(context.Context, auth.UserAuthenticator) (*tg.User, error)
}
type (
	loginRunnerFactory func(string) (loginRunner, error)
	gotdLoginRunner    struct{ opts login.SessionOptions }
)

func (r gotdLoginRunner) LoginCode(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error) {
	return login.CodeWithAuthenticator(ctx, r.opts, authenticator)
}

type loginMessenger struct{ bot botAPI }

func (m loginMessenger) SendText(ctx context.Context, chatID int64, text string) error {
	_, err := m.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), text))
	return err
}

func (m loginMessenger) DeleteInput(ctx context.Context, chatID int64, messageID int) error {
	return m.bot.DeleteMessage(ctx, &telego.DeleteMessageParams{ChatID: tu.ID(chatID), MessageID: messageID})
}

type loginProtocol struct{ runner loginRunner }

func (p loginProtocol) LoginCode(ctx context.Context, challenge ports.BotLoginChallenge) (*ports.LoginUser, error) {
	user, err := p.runner.LoginCode(ctx, protocolChallenge{challenge})
	if err != nil || user == nil {
		return nil, err
	}
	return &ports.LoginUser{ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName}, nil
}

type protocolChallenge struct{ ports.BotLoginChallenge }

func (p protocolChallenge) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return p.BotLoginChallenge.Code(ctx)
}

func (protocolChallenge) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, &auth.SignUpRequired{}
}

func (protocolChallenge) AcceptTermsOfService(_ context.Context, terms tg.HelpTermsOfService) error {
	return &auth.SignUpRequired{TermsOfService: terms}
}

func newLoginManagerWithFactory(ctx context.Context, bot botAPI, factory loginRunnerFactory) *account.BotLogin {
	return account.NewBotLogin(ctx, loginMessenger{bot}, func(namespace string) (ports.LoginRunner, error) {
		runner, err := factory(namespace)
		if err != nil {
			return nil, err
		}
		return loginProtocol{runner}, nil
	})
}
