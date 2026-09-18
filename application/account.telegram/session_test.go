package accounttelegram

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type sessionProbeFunc func(context.Context) (*ports.AccountIdentity, error)

func (f sessionProbeFunc) Probe(ctx context.Context) (*ports.AccountIdentity, error) { return f(ctx) }

type sessionResources struct{ stopped atomic.Bool }

func (*sessionResources) Drain(context.Context, types.AccountID) error { return nil }
func (r *sessionResources) Stop(context.Context) error                 { r.stopped.Store(true); return nil }

func sessionTestHost(t *testing.T, probe ports.SessionTransport) (*rte.Runtime, ports.AccountSession, *sessionResources) {
	t.Helper()
	registry := rte.NewRegistry()
	resources := &sessionResources{}
	require.NoError(t, RegisterResources(registry, resources))
	require.NoError(t, RegisterSession(registry, probe))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	for _, status := range host.Start(context.Background()) {
		require.Equal(t, rte.Running, status.State)
	}
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.AccountSessionName)
	require.NoError(t, err)
	return host, value.(ports.AccountSession), resources
}

func TestSessionPortRejectsWrongAccountAndInvalidIdentity(t *testing.T) {
	var calls atomic.Int32
	_, port, _ := sessionTestHost(t, sessionProbeFunc(func(context.Context) (*ports.AccountIdentity, error) {
		calls.Add(1)
		return &ports.AccountIdentity{}, nil
	}))
	_, err := port.Check(context.Background(), "other")
	require.ErrorContains(t, err, "account mismatch")
	require.Zero(t, calls.Load())
	_, err = port.Check(context.Background(), types.DefaultAccount)
	require.ErrorIs(t, err, ports.ErrSessionUnauthorized)
}

func TestSessionPortDrainsProbeBeforeAccountResources(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	host, port, resources := sessionTestHost(t, sessionProbeFunc(func(ctx context.Context) (*ports.AccountIdentity, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return &ports.AccountIdentity{ID: 7}, nil
	}))
	finished := make(chan error, 1)
	go func() { _, err := port.Check(context.Background(), types.DefaultAccount); finished <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := host.Stop(ctx)
	// Always release the transport even if the assertion below fails.
	stoppedEarly := resources.stopped.Load()
	close(release)
	require.Error(t, err)
	require.False(t, stoppedEarly)
	require.ErrorIs(t, <-finished, context.Canceled)
	require.NoError(t, host.Stop(context.Background()))
	require.True(t, resources.stopped.Load())
	_, err = port.Check(context.Background(), types.DefaultAccount)
	require.Error(t, err)
}
