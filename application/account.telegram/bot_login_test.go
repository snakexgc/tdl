package accounttelegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func TestLoginManagerCodeFlowSuccess(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, authenticator ports.BotLoginChallenge) (*ports.LoginUser, error) {
			phone, err := authenticator.Phone(ctx)
			require.NoError(t, err)
			require.Equal(t, "+8613800000000", phone)

			code, err := authenticator.Code(ctx)
			require.NoError(t, err)
			require.Equal(t, "12345", code)

			return &ports.LoginUser{ID: 42, Username: testCatalogAlice, FirstName: "Alice"}, nil
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.Eventually(t, func() bool { return manager.activeStage() == botPhoneStage }, time.Second, 10*time.Millisecond)
	require.True(t, manager.HandleInput(100, 100, "+86 138-0000-0000", 11))
	require.Eventually(t, func() bool { return manager.activeStage() == loginStageCode }, time.Second, 10*time.Millisecond)
	require.True(t, manager.HandleInput(100, 100, "23456", 12))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)

	require.Contains(t, bot.messagesText(), "登录成功！\nID: 42, Username: alice, Name: Alice")
	require.Equal(t, []int{12}, bot.deletedMessageIDs())
}

func TestBotLoginStopRetainsActiveFlowUntilProtocolReturns(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	manager := newTestLoginManager(&fakeBotAPI{}, &fakeLoginRunner{code: func(ctx context.Context, _ ports.BotLoginChallenge) (*ports.LoginUser, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}})
	require.NoError(t, manager.StartCode(1, 1))
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, manager.Stop(ctx), context.DeadlineExceeded)
	require.True(t, manager.Busy())
	require.Error(t, manager.StartCode(2, 2))
	close(release)
	require.NoError(t, manager.Stop(context.Background()))
	require.False(t, manager.Busy())
	require.Error(t, manager.StartCode(2, 2))
}

type blockedSuccessMessenger struct {
	fakeBotAPI
	entered, release chan struct{}
}

func (m *blockedSuccessMessenger) SendText(ctx context.Context, chatID int64, text string) error {
	if strings.HasPrefix(text, "登录成功！") {
		close(m.entered)
		<-m.release
	}
	return m.fakeBotAPI.SendText(ctx, chatID, text)
}

func TestBotLoginCanceledWhileSendingSuccessCannotActivateAccount(t *testing.T) {
	bot := &blockedSuccessMessenger{entered: make(chan struct{}), release: make(chan struct{})}
	manager := NewBotLogin(context.Background(), bot, func(string) (ports.LoginRunner, error) {
		return &fakeLoginRunner{code: func(context.Context, ports.BotLoginChallenge) (*ports.LoginUser, error) {
			return &ports.LoginUser{ID: 42}, nil
		}}, nil
	})
	activated := make(chan struct{}, 1)
	manager.SetOnSuccess(func(context.Context, *ports.LoginUser, string) { activated <- struct{}{} })
	require.NoError(t, manager.StartCode(1, 1, "Alice"))
	<-bot.entered
	require.True(t, manager.Cancel(1, 1))
	close(bot.release)
	require.NoError(t, manager.Stop(context.Background()))
	require.Empty(t, activated, "canceled login activated an account after the success message returned")
}

func TestBotLoginPreservesPasswordWhitespace(t *testing.T) {
	password := make(chan string, 1)
	manager := newTestLoginManager(&fakeBotAPI{}, &fakeLoginRunner{code: func(ctx context.Context, input ports.BotLoginChallenge) (*ports.LoginUser, error) {
		value, err := input.Password(ctx)
		password <- value
		return &ports.LoginUser{ID: 1}, err
	}})
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	require.NoError(t, manager.StartCode(1, 1))
	require.Eventually(t, func() bool { return manager.activeStage() == loginStagePassword }, time.Second, time.Millisecond)
	require.True(t, manager.HandleInput(1, 1, "  password  ", 3))
	select {
	case value := <-password:
		require.Equal(t, "  password  ", value)
	case <-time.After(time.Second):
		t.Fatal("password was not delivered")
	}
}

func TestLoginManagerRejectsConcurrentFlowAndCleansAfterCancel(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, _ ports.BotLoginChallenge) (*ports.LoginUser, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.ErrorIs(t, manager.StartCode(101, 101), errLoginBusy)
	require.True(t, manager.Cancel(100, 100))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
}

func TestLoginManagerRoutesInputOnlyFromActivePrivateChat(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, authenticator ports.BotLoginChallenge) (*ports.LoginUser, error) {
			phone, err := authenticator.Phone(ctx)
			require.NoError(t, err)
			require.Equal(t, "+8613800000000", phone)
			return &ports.LoginUser{ID: 42}, nil
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.Eventually(t, func() bool { return manager.activeStage() == botPhoneStage }, time.Second, 10*time.Millisecond)

	require.False(t, manager.HandleInput(101, 100, "wrong-user", 1))
	require.False(t, manager.HandleInput(100, 101, "wrong-chat", 2))
	require.True(t, manager.HandleInput(100, 100, "+8613800000000", 3))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
}

func TestLoginManagerInputTimeoutCleansActiveFlow(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, authenticator ports.BotLoginChallenge) (*ports.LoginUser, error) {
			_, err := authenticator.Phone(ctx)
			return nil, err
		},
	}
	manager := newTestLoginManager(bot, runner)
	manager.inputTimeout = 20 * time.Millisecond

	require.NoError(t, manager.StartCode(100, 100))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
	require.Contains(t, bot.messagesText(), "登录已超时，请重新发送 /login_code 用户名。")
}

func TestLoginManagerCancelDoesNotReportSuccessWithEmptyUser(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(ctx context.Context, _ ports.BotLoginChallenge) (*ports.LoginUser, error) {
			<-ctx.Done()
			return &ports.LoginUser{}, nil
		},
	}
	manager := newTestLoginManager(bot, runner)

	require.NoError(t, manager.StartCode(100, 100))
	require.True(t, manager.Cancel(100, 100))
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)

	messages := bot.messagesText()
	require.Contains(t, messages, "登录已取消。")
	require.False(t, hasMessagePrefix(messages, "登录成功！"))
}

func TestLoginManagerUsesNamespaceRunnerFactory(t *testing.T) {
	bot := &fakeBotAPI{}
	runner := &fakeLoginRunner{
		code: func(context.Context, ports.BotLoginChallenge) (*ports.LoginUser, error) {
			return &ports.LoginUser{ID: 42}, nil
		},
	}
	var factoryNamespace string
	manager := NewBotLogin(context.Background(), bot, func(namespace string) (ports.LoginRunner, error) {
		factoryNamespace = namespace
		return runner, nil
	})
	manager.inputTimeout = time.Second
	manager.flowTimeout = 2 * time.Second
	success := make(chan string, 1)
	manager.SetOnSuccess(func(ctx context.Context, _ *ports.LoginUser, namespace string) {
		require.NoError(t, ctx.Err())
		success <- namespace
	})

	require.NoError(t, manager.StartCode(100, 100, "Alice"))
	require.Equal(t, "Alice", factoryNamespace)
	require.Eventually(t, func() bool { return !manager.Busy() }, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return len(success) == 1 }, time.Second, 10*time.Millisecond)
	require.Equal(t, "Alice", <-success)
	require.Contains(t, bot.messagesText(), "用户 Alice 的登录流程已开始，发送 /cancel_login 可以取消。")
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

func newTestLoginManager(bot *fakeBotAPI, runner *fakeLoginRunner) *BotLogin {
	manager := NewBotLogin(context.Background(), bot, func(string) (ports.LoginRunner, error) {
		return runner, nil
	})
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
	code func(ctx context.Context, authenticator ports.BotLoginChallenge) (*ports.LoginUser, error)
}

func (f *fakeLoginRunner) LoginCode(ctx context.Context, authenticator ports.BotLoginChallenge) (*ports.LoginUser, error) {
	return f.code(ctx, authenticator)
}

type fakeBotAPI struct {
	mu       sync.Mutex
	messages []string
	deleted  []int
}

func (f *fakeBotAPI) SendText(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeBotAPI) DeleteInput(_ context.Context, _ int64, messageID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, messageID)
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
