package webui

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/app/login"
)

func TestWebLoginFlowKeepsRetryPromptUntilNextSubmit(t *testing.T) {
	flow := &webLoginFlow{
		stage:  "code",
		status: "验证码已发送，请直接输入 Telegram 收到的原始验证码。",
		codeCh: make(chan string, 1),
	}

	require.NoError(t, flow.authInputError(context.Background(), login.AuthInputCode, errors.New("bad code")))
	require.Equal(t, "code", flow.stage)
	require.Equal(t, "验证码不正确，请重新输入。", flow.status)
	require.Equal(t, "验证码不正确，请重新输入。", flow.errText)

	flow.prompt("code", "验证码已发送，请直接输入 Telegram 收到的原始验证码。")
	require.Equal(t, "验证码不正确，请重新输入。", flow.status)
	require.Equal(t, "验证码不正确，请重新输入。", flow.errText)

	require.NoError(t, flow.sendCode("12345"))
	require.Equal(t, "正在验证验证码...", flow.status)
	require.Empty(t, flow.errText)
}
