package bot

import (
	"context"
	"time"

	"github.com/fatih/color"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type botMessageSender interface {
	SendMessage(context.Context, *telego.SendMessageParams) (*telego.Message, error)
}
type botMessageEditor interface {
	EditMessageText(context.Context, *telego.EditMessageTextParams) (*telego.Message, error)
}
type trackedMessage = types.NotificationMessage

type botNotifier struct {
	host    *rte.Runtime
	service ports.Notifications
	account types.AccountID
}

type botNotificationTransport struct {
	sender botMessageSender
	editor botMessageEditor
}

func (t *botNotificationTransport) Send(ctx context.Context, chatID int64, text string) (int, error) {
	message, err := t.sender.SendMessage(ctx, tu.Message(tu.ID(chatID), text))
	if err != nil || message == nil {
		return 0, err
	}
	return message.MessageID, nil
}

func (t *botNotificationTransport) Edit(ctx context.Context, chatID int64, messageID int, text string) error {
	if t.editor == nil {
		return nil
	}
	_, err := t.editor.EditMessageText(ctx, &telego.EditMessageTextParams{ChatID: tu.ID(chatID), MessageID: messageID, Text: text})
	return err
}

func newBotNotifier(ctx context.Context, account types.AccountID, bot botMessageSender, chatIDs []int64) (*botNotifier, error) {
	if account == "" {
		account = types.DefaultAccount
	}
	editor, _ := bot.(botMessageEditor)
	host, service, err := application.NotificationHost(ctx, account, &botNotificationTransport{sender: bot, editor: editor}, chatIDs)
	if err != nil {
		return nil, err
	}
	return &botNotifier{host: host, service: service, account: account}, nil
}

func (n *botNotifier) Close() {
	if n == nil || n.host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := n.host.Stop(ctx); err != nil {
		color.Yellow("Failed to stop notifications: %v", err)
	}
}

func notificationContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	// Final task notifications may outlive a canceled task; the SWC still limits
	// their duration and cancels them with the Bot lifecycle.
	return context.WithoutCancel(ctx)
}
func (n *botNotifier) Notify(ctx context.Context, text string) { _ = n.SendAndTrack(ctx, text) }
func (n *botNotifier) SendAndTrack(ctx context.Context, text string) []trackedMessage {
	if n == nil || n.service == nil || text == "" {
		return nil
	}
	refs, err := n.service.Send(notificationContext(ctx), n.account, text)
	if err != nil {
		color.Yellow("Failed to send notification: %v", err)
	}
	return refs
}

func (n *botNotifier) EditTracked(ctx context.Context, refs []trackedMessage, text string) {
	if n == nil || n.service == nil || text == "" || len(refs) == 0 {
		return
	}
	if err := n.service.Edit(notificationContext(ctx), n.account, refs, text); err != nil {
		color.Yellow("Failed to edit notification: %v", err)
	}
}
