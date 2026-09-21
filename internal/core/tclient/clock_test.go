package tclient

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type calibratedClock struct{ offset atomic.Int64 }

func (c *calibratedClock) Now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }
func (*calibratedClock) Status() types.ClockStatus {
	return types.ClockStatus{Server: "invalid host:bad port"}
}

func TestTelegramUsesLiveRTEClockWithoutNetworkInConstructor(t *testing.T) {
	binding := new(rte.ClockBinding)
	ctx := rte.WithClock(context.Background(), binding)
	clock := networkClock(ctx)
	require.WithinDuration(t, time.Now(), clock.Now(), time.Second)
	source := new(calibratedClock)
	binding.Bind(source)
	source.offset.Store(int64(time.Hour))
	require.WithinDuration(t, time.Now().Add(time.Hour), clock.Now(), time.Second)
	client, err := New(ctx, Options{AppID: 1, AppHash: "test"})
	require.NoError(t, err)
	require.NotNil(t, client)
	source.offset.Store(int64(-time.Hour))
	require.WithinDuration(t, time.Now().Add(-time.Hour), clock.Now(), time.Second)
	timer := clock.Timer(time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C():
	case <-time.After(time.Second):
		t.Fatal("wall clock adjustment changed timer duration")
	}
	binding.Bind(nil)
	require.WithinDuration(t, time.Now(), clock.Now(), time.Second)
}
