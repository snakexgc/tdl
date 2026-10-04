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

// ProxyTimeProbe retries a measurement through the configured outbound proxy.
// Implementations must not silently bypass the proxy or expose its credentials.
type ProxyTimeProbe interface {
	QueryViaProxy(context.Context, string, string, time.Duration) (types.TimeSample, error)
}
