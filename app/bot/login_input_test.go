package bot

import (
	"testing"

	"github.com/mymmrac/telego"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

type capturingLogin struct {
	ports.BotLogin
	input string
}

func (l *capturingLogin) HandleInput(_, _ int64, text string, _ int) bool {
	l.input = text
	return true
}

func TestMessageDispatchPreservesLoginPassword(t *testing.T) {
	login := &capturingLogin{}
	message := &telego.Message{From: &telego.User{ID: 1}, Chat: telego.Chat{ID: 1, Type: telego.ChatTypePrivate}, Text: "  password with spaces\t "}
	require.NoError(t, handleAllowedMessage(nil, message, login, nil, nil, nil, nil, nil, nil, "bound", nil))
	require.Equal(t, message.Text, login.input)
}
