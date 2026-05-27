package watch

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
)

type loggingUpdateHandler struct {
	inner telegram.UpdateHandler
}

func (h *loggingUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	switch u := updates.(type) {
	case *tg.Updates:
		types := make([]string, 0, len(u.Updates))
		for _, upd := range u.Updates {
			types = append(types, fmt.Sprintf("%T", upd))
		}
		logctx.From(ctx).Debug("Updates batch received",
			zap.String("container", "Updates"),
			zap.Int("count", len(u.Updates)),
			zap.Strings("types", types),
			zap.Int("users", len(u.Users)),
			zap.Int("chats", len(u.Chats)))

	case *tg.UpdatesCombined:
		types := make([]string, 0, len(u.Updates))
		for _, upd := range u.Updates {
			types = append(types, fmt.Sprintf("%T", upd))
		}
		logctx.From(ctx).Debug("UpdatesCombined batch received",
			zap.String("container", "UpdatesCombined"),
			zap.Int("count", len(u.Updates)),
			zap.Strings("types", types),
			zap.Int("users", len(u.Users)),
			zap.Int("chats", len(u.Chats)))

	case *tg.UpdateShort:
		updateType := fmt.Sprintf("%T", u.Update)
		logctx.From(ctx).Debug("UpdateShort received",
			zap.String("type", updateType),
			zap.Int("date", u.Date))

	case *tg.UpdatesTooLong:
		logctx.From(ctx).Debug("UpdatesTooLong received")

	default:
		updateType := fmt.Sprintf("%T", u)
		logctx.From(ctx).Debug("Unknown updates type",
			zap.String("type", updateType))
	}

	return h.inner.Handle(ctx, updates)
}
