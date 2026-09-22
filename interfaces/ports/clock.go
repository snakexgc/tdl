package ports

import (
	"context"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

const ClockName = "time.clock"

// Clock provides process wall time. Reads are local and must never wait for a
// network request. Elapsed durations and deadlines use the system monotonic clock.
type Clock interface {
	Now() time.Time
	Status() types.ClockStatus
}

// TimeProbe performs one cancellable network measurement, without changing OS time.
type TimeProbe interface {
	Query(context.Context, string, time.Duration) (types.TimeSample, error)
}
