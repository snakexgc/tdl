package rte

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

// ClockBinding forwards the process time port across independently owned hosts.
// Its zero value uses system time, so neither startup nor disabled time sync
// prevents consumers from reading the clock. Bind is for the composition root.
type ClockBinding struct{ source atomic.Pointer[ports.Clock] }

func (b *ClockBinding) Bind(source ports.Clock) {
	if source == nil {
		b.source.Store(nil)
		return
	}
	b.source.Store(&source)
}

func (b *ClockBinding) clock() ports.Clock {
	if source := b.source.Load(); source != nil {
		return *source
	}
	return ports.ClockFrom(context.Background())
}
func (b *ClockBinding) Now() time.Time            { return b.clock().Now() }
func (b *ClockBinding) Status() types.ClockStatus { return b.clock().Status() }

func WithClock(ctx context.Context, clock ports.Clock) context.Context {
	return ports.WithClock(ctx, clock)
}

// ClockFrom is the adapter entry point to the same port exposed as Kernel.Clock.
func ClockFrom(ctx context.Context) ports.Clock {
	return ports.ClockFrom(ctx)
}

func Now(ctx context.Context) time.Time { return ClockFrom(ctx).Now() }
