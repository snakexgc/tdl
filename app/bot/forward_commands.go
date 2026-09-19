package bot

import (
	"context"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/application/forwarder"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

// Standalone Bot compatibility binds capabilities and converts Telegram DTOs.
// Managed production resolves the forwarder's declared command port directly.
func handleForwardCommand(ctx *th.Context, msg *telego.Message, text string, queue ports.ForwardTasks, account types.AccountID) (bool, error) {
	if commandName(text) != botCmdForward {
		return false, nil
	}
	command := forwarder.NewCommand(ctx, account, queue, application.ValidateMessageLink, func() forwarder.CommandSettings {
		cfg := botConfiguration(ctx)
		if cfg == nil {
			return forwarder.CommandSettings{Mode: config.ForwardModeDefault}
		}
		return forwarder.CommandSettings{Target: cfg.Forward.Target, Mode: config.EffectiveForwardMode(cfg), Silent: cfg.Forward.Silent}
	})
	defer command.Stop(context.Background())
	request := types.ConsoleRequest{Account: account, Name: "forward", Text: text, Private: msg.Chat.Type == telego.ChatTypePrivate}
	if msg.ReplyToMessage != nil {
		request.ReplyText = msg.ReplyToMessage.Text + "\n" + msg.ReplyToMessage.Caption
	}
	response, err := command.Execute(ctx, request)
	if err != nil {
		return true, err
	}
	return true, sendMessage(ctx, msg.Chat.ID, response.Text)
}
