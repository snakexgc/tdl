package bot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/mymmrac/telego"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/app/login"
)

func TestLoginManagerCodeFlowSuccess(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error) {
			phone, err := authenticator.Phone(ctx)
			require.NoError(t, err)
			require.Equal(t, "+8613800000000", phone)

			code, err := authenticator.Code(ctx, nil)
			require.NoError(t, err)
			require.Equal(t, "12345", code)

			return &tg.User{ID: 42, Username: "alice", FirstName: "Alice"}, nil
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.Eventually(t, func() bool { return manager.activeStage() == "phone" }, time.Second, 10*time.Millisecond)
	require.True(t, manager.HandleInput(100, 100, "+86 138-0000-0000", 11))
	require.Eventually(t, func() bool { return manager.activeStage() == "code" }, time.Second, 10*time.Millisecond)
	require.True(t, manager.HandleInput(100, 100, "23456", 12))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)

	require.Contains(t, bot.messagesText(), "登录成功！\nID: 42, Username: alice, Name: Alice")
	require.Equal(t, []int{12}, bot.deletedMessageIDs())
}

func TestLoginManagerRejectsConcurrentFlowAndCleansAfterCancel(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, _ auth.UserAuthenticator) (*tg.User, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.ErrorIs(t, manager.StartQR(101, 101), errLoginBusy)
	require.True(t, manager.Cancel(100, 100))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
}

func TestLoginManagerRoutesInputOnlyFromActivePrivateChat(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error) {
			phone, err := authenticator.Phone(ctx)
			require.NoError(t, err)
			require.Equal(t, "+8613800000000", phone)
			return &tg.User{ID: 42}, nil
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.Eventually(t, func() bool { return manager.activeStage() == "phone" }, time.Second, 10*time.Millisecond)

	require.False(t, manager.HandleInput(101, 100, "wrong-user", 1))
	require.False(t, manager.HandleInput(100, 101, "wrong-chat", 2))
	require.True(t, manager.HandleInput(100, 100, "+8613800000000", 3))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
}

func TestLoginManagerInputTimeoutCleansActiveFlow(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error) {
			_, err := authenticator.Phone(ctx)
			return nil, err
		},
	}
	manager := newTestLoginManager(bot, runner)
	manager.inputTimeout = 20 * time.Millisecond

	require.NoError(t, manager.StartCode(100, 100))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
	require.Contains(t, bot.messagesText(), "登录已超时，请重新发送 /login_code 用户名 或 /login_qr 用户名。")
}

func TestLoginManagerQRCancelDoesNotReportSuccessWithEmptyUser(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		qr: func(ctx context.Context, _ login.QRShowFunc, _ login.PasswordFunc) (*tg.User, error) {
			<-ctx.Done()
			return &tg.User{}, nil
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartQR(100, 100))
	require.True(t, manager.Cancel(100, 100))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)

	messages := bot.messagesText()
	require.Contains(t, messages, "登录已取消。")
	require.False(t, hasMessagePrefix(messages, "登录成功！"))
}

func TestLoginManagerUsesNamespaceRunnerFactory(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		qr: func(context.Context, login.QRShowFunc, login.PasswordFunc) (*tg.User, error) {
			return &tg.User{ID: 42}, nil
		},
	}
	var factoryNamespace string
	manager := newLoginManagerWithFactory(context.Background(), bot, func(namespace string) (loginRunner, error) {
		factoryNamespace = namespace
		return runner, nil
	})
	manager.inputTimeout = time.Second
	manager.flowTimeout = 2 * time.Second
	success := make(chan string, 1)
	manager.SetOnSuccess(func(_ *tg.User, namespace string) {
		success <- namespace
	})

	require.NoError(t, manager.StartQR(100, 100, "Alice"))
	require.Equal(t, "Alice", factoryNamespace)
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return len(success) == 1 }, time.Second, 10*time.Millisecond)
	require.Equal(t, "Alice", <-success)
	require.Contains(t, bot.messagesText(), "用户 Alice 的登录流程已开始，发送 /cancel_login 可以取消。")
}

func TestLoginNamespaceFromCommand(t *testing.T) {
	namespace, err := loginNamespaceFromCommand("/login_code Alice")
	require.NoError(t, err)
	require.Equal(t, "Alice", namespace)

	_, err = loginNamespaceFromCommand("/login_qr")
	require.Error(t, err)

	_, err = loginNamespaceFromCommand("/login_qr alice1")
	require.Error(t, err)
}

func TestUnmaskLoginCode(t *testing.T) {
	code, ok := unmaskLoginCode("23456")
	require.True(t, ok)
	require.Equal(t, "12345", code)

	code, ok = unmaskLoginCode("00000")
	require.True(t, ok)
	require.Equal(t, "99999", code)

	_, ok = unmaskLoginCode("12a45")
	require.False(t, ok)
}

func newTestLoginManager(bot *fakeBotAPI, runner *fakeLoginRunner) *loginManager {
	manager := newLoginManager(context.Background(), bot, runner)
	manager.inputTimeout = time.Second
	manager.flowTimeout = 2 * time.Second
	return manager
}

func hasMessagePrefix(messages []string, prefix string) bool {
	for _, message := range messages {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}

type fakeLoginRunner struct {
	code func(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error)
	qr   func(ctx context.Context, show login.QRShowFunc, password login.PasswordFunc) (*tg.User, error)
}

func (f *fakeLoginRunner) LoginCode(ctx context.Context, authenticator auth.UserAuthenticator) (*tg.User, error) {
	return f.code(ctx, authenticator)
}

func (f *fakeLoginRunner) LoginQR(ctx context.Context, show login.QRShowFunc, password login.PasswordFunc) (*tg.User, error) {
	if f.qr != nil {
		return f.qr(ctx, show, password)
	}
	return nil, context.Canceled
}

type fakeBotAPI struct {
	mu       sync.Mutex
	messages []string
	photos   []string
	deleted  []int
}

func (f *fakeBotAPI) SendMessage(_ context.Context, params *telego.SendMessageParams) (*telego.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.messages = append(f.messages, params.Text)
	return &telego.Message{MessageID: len(f.messages)}, nil
}

func (f *fakeBotAPI) SendPhoto(_ context.Context, params *telego.SendPhotoParams) (*telego.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.photos = append(f.photos, params.Caption)
	return &telego.Message{MessageID: len(f.photos)}, nil
}

func (f *fakeBotAPI) DeleteMessage(_ context.Context, params *telego.DeleteMessageParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, params.MessageID)
	return nil
}

func (f *fakeBotAPI) messagesText() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.messages...)
}

func (f *fakeBotAPI) deletedMessageIDs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]int(nil), f.deleted...)
}
