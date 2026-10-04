package logging

import (
	"time"

	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/ports"
)

// WithClock uses the shared application time for log records. Zap's internal
// periodic maintenance still uses real timers rather than adjustable wall time.
func WithClock(logger *zap.Logger, clock ports.Clock) *zap.Logger {
	return logger.WithOptions(zap.WithClock(logClock{clock}))
}

type logClock struct{ ports.Clock }

func (c logClock) Now() time.Time { return c.Clock.Now().In(time.Local) }

func (logClock) NewTicker(interval time.Duration) *time.Ticker {
	return time.NewTicker(interval)
}
