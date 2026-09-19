package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	consolebot "github.com/snakexgc/tdl/application/console.bot"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func declaredCommandHandlers(commands []types.ConsoleCommand, resolve func(string, string) (any, error)) []ports.ConsoleContribution {
	if resolve == nil {
		return nil // Standalone adapters supply their own component capabilities.
	}
	contributions := []ports.ConsoleContribution{}
	for _, command := range commands {
		if command.Port == "" {
			continue
		}
		contributions = append(contributions, ports.ConsoleContribution{Commands: []types.ConsoleCommand{command}, Handler: ports.ConsoleCommandFunc(func(ctx context.Context, request types.ConsoleRequest) (types.ConsoleResponse, error) {
			if resolve == nil {
				return types.ConsoleResponse{}, fmt.Errorf("component command is unavailable")
			}
			value, err := resolve(command.Owner, command.Port)
			if err != nil {
				return types.ConsoleResponse{}, err
			}
			handler, ok := value.(ports.ConsoleCommandHandler)
			if !ok {
				return types.ConsoleResponse{}, fmt.Errorf("invalid component command port")
			}
			return handler.Execute(ctx, request)
		})})
	}
	return contributions
}

// Only this composition table binds legacy Telegram formatting to business
// ports. New components can supply a DTO handler through CommandContributions.
func commandAdapters(ctx *th.Context, msg *telego.Message, login ports.BotLogin, reboot func(), update *tdlUpdateController, aria aria2ControllerFactory, local internalDownloadControllerFactory, maintenance ports.KVMaintenance, account types.AccountID, forward ports.ForwardTasks) map[string]ports.ConsoleCommandHandler {
	wrap := func(run func(string) (bool, error)) ports.ConsoleCommandHandler {
		return ports.ConsoleCommandFunc(func(_ context.Context, request types.ConsoleRequest) (types.ConsoleResponse, error) {
			handled, err := run(canonicalCommandText(request))
			if !handled && err == nil {
				err = fmt.Errorf("command %s has no compatible handler", request.Name)
			}
			return types.ConsoleResponse{}, err
		})
	}
	return map[string]ports.ConsoleCommandHandler{
		"download.control":    wrap(func(text string) (bool, error) { return handleDownloadCommand(ctx, msg, text, aria, local) }),
		"storage.maintenance": wrap(func(text string) (bool, error) { return handleKVCommand(ctx, msg, text, maintenance, account) }),
		"update.self":         wrap(func(text string) (bool, error) { return handleUpdateCommand(ctx, msg, text, update) }),
		"forwarder":           wrap(func(text string) (bool, error) { return handleForwardCommand(ctx, msg, text, forward, account) }),
		"account.telegram":    wrap(func(text string) (bool, error) { return handleAccountCommand(ctx, msg, text, login) }),
		"console.bot": ports.ConsoleCommandFunc(func(context.Context, types.ConsoleRequest) (types.ConsoleResponse, error) {
			if err := sendMessage(ctx, msg.Chat.ID, "正在重启程序，稍后会收到新的启动状态。"); err != nil {
				return types.ConsoleResponse{}, err
			}
			if reboot != nil {
				reboot()
			}
			return types.ConsoleResponse{}, nil
		}),
	}
}

func canonicalCommandText(request types.ConsoleRequest) string {
	text := strings.TrimSpace(request.Text)
	if space := strings.IndexAny(text, " \t\r\n"); space >= 0 {
		return "/" + request.Name + text[space:]
	}
	return "/" + request.Name
}

func dispatchConsoleCommand(ctx *th.Context, msg *telego.Message, policy ports.Console, account types.AccountID, handlers map[string]ports.ConsoleCommandHandler, extra []ports.ConsoleContribution) (types.ConsoleResponse, bool, error) {
	byName := map[string]ports.ConsoleCommandHandler{}
	for _, contribution := range extra {
		for _, command := range contribution.Commands {
			byName[command.Name] = contribution.Handler
		}
	}
	contributions := []ports.ConsoleContribution{}
	for _, command := range policy.Commands() {
		handler := handlers[command.Owner]
		if supplied := byName[command.Name]; supplied != nil {
			handler = supplied
		}
		if handler == nil {
			return types.ConsoleResponse{}, true, fmt.Errorf("command %s has no handler", command.Name)
		}
		contributions = append(contributions, ports.ConsoleContribution{Commands: []types.ConsoleCommand{command}, Handler: handler})
	}
	dispatcher, err := consolebot.NewDispatcher(policy, contributions)
	if err != nil {
		return types.ConsoleResponse{}, true, err
	}
	replyText := ""
	if msg.ReplyToMessage != nil {
		replyText = msg.ReplyToMessage.Text + "\n" + msg.ReplyToMessage.Caption
	}
	request := types.ConsoleRequest{Account: account, UserID: msg.From.ID, ChatID: msg.Chat.ID, MessageID: msg.MessageID, Name: strings.TrimPrefix(commandName(msg.Text), "/"), Text: msg.Text, ReplyText: replyText, Private: msg.Chat.Type == telego.ChatTypePrivate}
	handled, response, err := dispatcher.Dispatch(ctx, request)
	return response, handled, err
}
