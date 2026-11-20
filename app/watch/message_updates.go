package watch

import (
	"context"
	"errors"

	"github.com/gotd/td/tg"
)

func (w *Watcher) observeListen(ctx context.Context, entities tg.Entities, message tg.MessageClass) error {
	if w.opts.Listener == nil || !w.downloadEnabled() {
		return nil
	}
	if message, ok := message.(*tg.Message); ok {
		return w.opts.Listener.Observe(ctx, w.listenMessage(message, entities))
	}
	return nil
}

func (w *Watcher) onNewMessage(ctx context.Context, entities tg.Entities, update *tg.UpdateNewMessage) error {
	listenErr := w.observeListen(ctx, entities, update.Message)
	forwardErr := w.onNewMessageForward(ctx, entities, update)
	return errors.Join(listenErr, forwardErr)
}

func (w *Watcher) onNewChannelMessage(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
	listenErr := w.observeListen(ctx, entities, update.Message)
	forwardErr := w.onNewChannelMessageForward(ctx, entities, update)
	return errors.Join(listenErr, forwardErr)
}

func (w *Watcher) onEditMessageUpdate(ctx context.Context, entities tg.Entities, update *tg.UpdateEditMessage) error {
	listenErr := w.observeListen(ctx, entities, update.Message)
	reactionErr := w.onEditMessage(ctx, entities, update)
	return errors.Join(listenErr, reactionErr)
}

func (w *Watcher) onEditChannelMessageUpdate(ctx context.Context, entities tg.Entities, update *tg.UpdateEditChannelMessage) error {
	listenErr := w.observeListen(ctx, entities, update.Message)
	reactionErr := w.onEditChannelMessage(ctx, entities, update)
	return errors.Join(listenErr, reactionErr)
}
