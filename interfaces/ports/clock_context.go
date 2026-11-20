package ports

import (
	"context"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

type clockKey struct{}

type systemClock struct{}

// Go uses each platform's native clock. Containers read their visible kernel
// clock; the configured timezone changes presentation, not the time instant.
func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Status() types.ClockStatus {
	return types.ClockStatus{Source: types.ClockSourceSystem}
}

// WithClock carries the shared time port into protocol and storage adapters
// without making those lower layers depend on the runtime implementation.
func WithClock(ctx context.Context, clock Clock) context.Context {
	return context.WithValue(ctx, clockKey{}, clock)
}

func ClockFrom(ctx context.Context) Clock {
	if ctx != nil {
		if clock, ok := ctx.Value(clockKey{}).(Clock); ok && clock != nil {
			return clock
		}
	}
	return systemClock{}
}

func Now(ctx context.Context) time.Time { return ClockFrom(ctx).Now() }
