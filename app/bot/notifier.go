package bot

import (
	"context"
	"sync"

	"github.com/fatih/color"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

type botMessageSender interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
}

type botMessageEditor interface {
	EditMessageText(ctx context.Context, params *telego.EditMessageTextParams) (*telego.Message, error)
}

// trackedMessage holds a chat ID and message ID for a sent message that may be later edited.
type trackedMessage struct {
	chatID    int64
	messageID int
}

type botNotifier struct {
	mu      sync.RWMutex
	bot     botMessageSender
	editor  botMessageEditor // nil when bot does not implement EditMessageText
	chatIDs []int64
}

func newBotNotifier(bot botMessageSender, chatIDs []int64) *botNotifier {
	editor, _ := bot.(botMessageEditor)
	return &botNotifier{
		bot:     bot,
		editor:  editor,
		chatIDs: uniqueInt64s(chatIDs),
	}
}

func (n *botNotifier) Notify(ctx context.Context, text string) {
	if n == nil || n.bot == nil || text == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithoutCancel(ctx)

	n.mu.RLock()
	chatIDs := append([]int64(nil), n.chatIDs...)
	n.mu.RUnlock()

	for _, chatID := range chatIDs {
		if _, err := n.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), text)); err != nil {
			color.Yellow("Failed to notify user %d: %v", chatID, err)
		}
	}
}

// SendAndTrack sends text to all chat IDs and returns the resulting message references.
// Used for live-progress messages that will later be edited.
func (n *botNotifier) SendAndTrack(ctx context.Context, text string) []trackedMessage {
	if n == nil || n.bot == nil || text == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithoutCancel(ctx)

	n.mu.RLock()
	chatIDs := append([]int64(nil), n.chatIDs...)
	n.mu.RUnlock()

	var tracked []trackedMessage
	for _, chatID := range chatIDs {
		msg, err := n.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), text))
		if err != nil {
			color.Yellow("Failed to send tracked message to user %d: %v", chatID, err)
			continue
		}
		if msg != nil {
			tracked = append(tracked, trackedMessage{chatID: chatID, messageID: msg.MessageID})
		}
	}
	return tracked
}

// EditTracked edits all previously tracked messages with the new text.
func (n *botNotifier) EditTracked(ctx context.Context, tracked []trackedMessage, text string) {
	if n == nil || n.editor == nil || len(tracked) == 0 || text == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithoutCancel(ctx)

	for _, ref := range tracked {
		if _, err := n.editor.EditMessageText(ctx, &telego.EditMessageTextParams{
			ChatID:    tu.ID(ref.chatID),
			MessageID: ref.messageID,
			Text:      text,
		}); err != nil {
			color.Yellow("Failed to edit tracked message for user %d msg %d: %v", ref.chatID, ref.messageID, err)
		}
	}
}

func (n *botNotifier) UpdateChatIDs(chatIDs []int64) {
	if n == nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.chatIDs = uniqueInt64s(chatIDs)
}

func (n *botNotifier) ChatIDs() []int64 {
	if n == nil {
		return nil
	}

	n.mu.RLock()
	defer n.mu.RUnlock()
	return append([]int64(nil), n.chatIDs...)
}

func uniqueInt64s(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[int64]struct{}, len(values))
	unique := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
