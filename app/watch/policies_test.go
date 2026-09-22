package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte"
)

func TestControllerDoesNotOwnInjectedPolicyHost(t *testing.T) {
	ctx := context.Background()
	initial := Options{Template: "F", FilenameMaxLength: 255}
	host, filter, naming, err := startPolicies(ctx, "", initial)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	initial.Filter, initial.Naming = filter, naming
	old := runControllerWatch
	t.Cleanup(func() { runControllerWatch = old })
	ready := make(chan Options, 1)
	runControllerWatch = func(ctx context.Context, opts Options) error {
		ready <- opts
		<-ctx.Done()
		return nil
	}
	c := NewController(ctx, initial, nil)
	t.Cleanup(c.Stop)
	for range 2 {
		require.True(t, c.Start())
		select {
		case opts := <-ready:
			require.Same(t, naming, opts.Naming)
			require.Same(t, filter, opts.Filter)
		case <-time.After(time.Second):
			t.Fatal("watch did not start")
		}
		c.Stop()
		for _, status := range host.Statuses() {
			require.Equal(t, rte.Running, status.State)
		}
	}
}
