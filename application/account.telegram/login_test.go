package accounttelegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
)

func TestLoginPortRetryAndSnapshot(t *testing.T) {
	password := make(chan string, 1)
	m := NewLogin(LoginOptions{
		Prepare: func(namespace string) (string, error) { return namespace, nil },
		Authenticate: func(ctx context.Context, namespace, phone string, challenge ports.LoginChallenge) (map[string]any, error) {
			code, err := challenge.Code(ctx)
			if err != nil {
				return nil, err
			}
			if code == "wrong" {
				if err := challenge.AuthInputError(ctx, loginStageCode, errors.New("invalid")); err != nil {
					return nil, err
				}
				if _, err := challenge.Code(ctx); err != nil {
					return nil, err
				}
			}
			value, err := challenge.Password(ctx)
			if err != nil {
				return nil, err
			}
			password <- value
			return map[string]any{"id": int64(12), "namespace": namespace, "phone": phone}, nil
		},
	})
	registry := rte.NewRegistry()
	require.NoError(t, RegisterLogin(registry, m))
	host, err := registry.Build("alice", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.AccountLoginName)
	require.NoError(t, err)
	port := value.(ports.AccountLogin)
	require.NoError(t, port.StartPhone(context.Background(), "+1 (234)", "bob"))
	require.Eventually(t, func() bool { return port.Status().Stage == loginStageCode }, time.Second, time.Millisecond)
	require.ErrorContains(t, port.SubmitPassword("secret"), "not been requested")
	require.NoError(t, port.SubmitCode("wrong"))
	require.Eventually(t, func() bool { return port.Status().Error != "" }, time.Second, time.Millisecond)
	require.NoError(t, port.SubmitCode("correct"))
	require.Eventually(t, func() bool { return port.Status().Stage == loginStagePassword }, time.Second, time.Millisecond)
	require.NoError(t, port.SubmitPassword("  secret  "))
	require.Equal(t, "  secret  ", <-password)
	require.Eventually(t, func() bool { return port.Status().Stage == loginStageDone }, time.Second, time.Millisecond)
	snapshot := port.Status()
	require.Equal(t, "bob", snapshot.User["namespace"])
	require.Equal(t, "+1234", snapshot.User["phone"])
	snapshot.User["id"] = int64(999)
	require.Equal(t, int64(12), port.Status().User["id"])
}

func TestLoginCancellationPreventsCompletion(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	completed := false
	m := NewLogin(LoginOptions{Complete: func(context.Context, string, map[string]any) (bool, error) {
		completed = true
		return false, nil
	}})
	require.NoError(t, m.start(context.Background(), "phone", "alice", func(ctx context.Context, _ *loginFlow) (map[string]any, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return map[string]any{"id": int64(12)}, nil
	}, nil))
	<-entered
	m.Cancel()
	close(release)
	require.NoError(t, m.Stop(context.Background()))
	require.False(t, completed)
	require.Equal(t, loginStageFailed, m.Status().Stage)
}

func TestWebLoginShutdownWaitsForAuthenticationAndClosesAdmission(t *testing.T) {
	m := NewLogin(LoginOptions{})
	entered, release := make(chan struct{}), make(chan struct{})
	run := func(ctx context.Context, _ *loginFlow) (map[string]any, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}
	require.NoError(t, m.start(context.Background(), "phone", "default", run, nil))
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, m.Stop(ctx), context.DeadlineExceeded)
	require.ErrorContains(t, m.start(context.Background(), "phone", "default", run, nil), "stopped")
	close(release)
	require.NoError(t, m.Stop(context.Background()))
	require.False(t, m.Status().Active)
}

func TestWebCodeAuthenticatorPromptsOnlyAfterCodeSent(t *testing.T) {
	flow := &loginFlow{
		stage:  loginStageSendingCode,
		status: loginStatusSendingCode,
		codeCh: make(chan string, 1),
	}
	authenticator := loginChallenge{flow: flow}

	require.EqualError(t, flow.sendCode("12345"), "验证码尚未发送，请稍候。")
	require.Equal(t, loginStageSendingCode, flow.stage)
	require.Equal(t, loginStatusSendingCode, flow.status)

	type codeResult struct {
		code string
		err  error
	}
	result := make(chan codeResult, 1)
	go func() {
		code, err := authenticator.Code(context.Background())
		result <- codeResult{code: code, err: err}
	}()

	require.Eventually(t, func() bool {
		flow.mu.Lock()
		defer flow.mu.Unlock()
		return flow.stage == loginStageCode && flow.status == loginStatusCodeSent
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, flow.sendCode("12345"))
	got := <-result
	require.NoError(t, got.err)
	require.Equal(t, "12345", got.code)
}

func TestWebLoginFlowKeepsRetryPromptUntilNextSubmit(t *testing.T) {
	flow := &loginFlow{
		stage:  loginStageCode,
		status: "验证码已发送，请直接输入 Telegram 收到的原始验证码。",
		codeCh: make(chan string, 1),
	}

	require.NoError(t, flow.authInputError(context.Background(), loginStageCode, errors.New("bad code")))
	require.Equal(t, loginStageCode, flow.stage)
	require.Equal(t, "验证码不正确，请重新输入。", flow.status)
	require.Equal(t, "验证码不正确，请重新输入。", flow.errText)

	flow.prompt(loginStageCode, "验证码已发送，请直接输入 Telegram 收到的原始验证码。")
	require.Equal(t, "验证码不正确，请重新输入。", flow.status)
	require.Equal(t, "验证码不正确，请重新输入。", flow.errText)

	require.NoError(t, flow.sendCode("12345"))
	require.Equal(t, "正在验证验证码...", flow.status)
	require.Empty(t, flow.errText)
}
