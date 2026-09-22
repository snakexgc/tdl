package tclient

import (
	"context"
	"time"

	"github.com/gotd/td/clock"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
)

// Telegram shares the RTE wall clock. Timers retain system monotonic semantics;
// correcting UTC must not move request deadlines or trigger timeout storms.
type networkTime struct{ ports.Clock }

func (networkTime) Timer(d time.Duration) clock.Timer   { return clock.System.Timer(d) }
func (networkTime) Ticker(d time.Duration) clock.Ticker { return clock.System.Ticker(d) }

func networkClock(ctx context.Context) clock.Clock { return networkTime{rte.ClockFrom(ctx)} }
