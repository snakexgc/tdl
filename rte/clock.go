package rte

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type systemClock struct{}

func (systemClock) Now() time.Time            { return time.Now() }
func (systemClock) Status() types.ClockStatus { return types.ClockStatus{} }

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
	return systemClock{}
}
func (b *ClockBinding) Now() time.Time            { return b.clock().Now() }
func (b *ClockBinding) Status() types.ClockStatus { return b.clock().Status() }

type clockKey struct{}

func WithClock(ctx context.Context, clock ports.Clock) context.Context {
	return context.WithValue(ctx, clockKey{}, clock)
}

// ClockFrom is the adapter entry point to the same port exposed as Kernel.Clock.
func ClockFrom(ctx context.Context) ports.Clock {
	if ctx != nil {
		if clock, ok := ctx.Value(clockKey{}).(ports.Clock); ok && clock != nil {
			return clock
		}
	}
	return systemClock{}
}

func Now(ctx context.Context) time.Time { return ClockFrom(ctx).Now() }
