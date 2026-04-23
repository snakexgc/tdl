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

type botNotifier struct {
	mu      sync.RWMutex
	bot     botMessageSender
	chatIDs []int64
}

func newBotNotifier(bot botMessageSender, chatIDs []int64) *botNotifier {
	return &botNotifier{
		bot:     bot,
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

func (n *botNotifier) UpdateChatIDs(chatIDs []int64) {
	if n == nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.chatIDs = uniqueInt64s(chatIDs)
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
