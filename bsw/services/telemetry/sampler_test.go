package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestViewersShareSamples(t *testing.T) {
	s := New(time.Minute)
	var calls atomic.Int64
	source := func(context.Context) (any, error) { calls.Add(1); return map[string]int{"value": 1}, nil }
	var work sync.WaitGroup
	for range 30 {
		work.Go(func() { _, _ = s.Read(context.Background(), "speed", source) })
	}
	work.Wait()
	require.EqualValues(t, 1, calls.Load())
	_, err := s.Read(context.Background(), "other", source)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
}
