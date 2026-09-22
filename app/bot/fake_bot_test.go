package bot

import (
	"context"
	"sync"

	"github.com/mymmrac/telego"
)

type fakeBotAPI struct {
	mu       sync.Mutex
	messages []string
	deleted  []int
}

func (f *fakeBotAPI) SendMessage(_ context.Context, params *telego.SendMessageParams) (*telego.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.messages = append(f.messages, params.Text)
	return &telego.Message{MessageID: len(f.messages)}, nil
}

func (f *fakeBotAPI) DeleteMessage(_ context.Context, params *telego.DeleteMessageParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, params.MessageID)
	return nil
}

func (f *fakeBotAPI) messagesText() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.messages...)
}
