package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	NotificationsName         = "notify.telegram"
	NotificationTransportName = "notification.transport"
)

type Notifications interface {
	// Enqueue accepts a best-effort notification into a bounded event queue.
	// It reports admission errors; successful admission is not delivery confirmation.
	Enqueue(context.Context, types.AccountID, string) error
	Send(context.Context, types.AccountID, string) ([]types.NotificationMessage, error)
	Edit(context.Context, types.AccountID, []types.NotificationMessage, string) error
}

// NotificationTransport adapts protocol-specific messages at the host boundary.
type NotificationTransport interface {
	Send(context.Context, int64, string) (int, error)
	Edit(context.Context, int64, int, string) error
}
