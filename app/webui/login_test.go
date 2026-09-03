package webui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/app/login"
)

func TestWebCodeAuthenticatorPromptsOnlyAfterCodeSent(t *testing.T) {
	flow := &webLoginFlow{
		stage:  loginStageSendingCode,
		status: loginStatusSendingCode,
		codeCh: make(chan string, 1),
	}
	authenticator := webCodeAuthenticator{flow: flow, phone: "+8613800000000"}

	require.EqualError(t, flow.sendCode("12345"), "验证码尚未发送，请稍候。")
	require.Equal(t, loginStageSendingCode, flow.stage)
	require.Equal(t, loginStatusSendingCode, flow.status)

	type codeResult struct {
		code string
		err  error
	}
	result := make(chan codeResult, 1)
	go func() {
		code, err := authenticator.Code(context.Background(), &tg.AuthSentCode{})
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
	flow := &webLoginFlow{
		stage:  loginStageCode,
		status: "验证码已发送，请直接输入 Telegram 收到的原始验证码。",
		codeCh: make(chan string, 1),
	}

	require.NoError(t, flow.authInputError(context.Background(), login.AuthInputCode, errors.New("bad code")))
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
