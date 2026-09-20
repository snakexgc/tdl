package updater

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func TestComponentStopCancelsAndWaitsForUpdate(t *testing.T) {
	registry := rte.NewRegistry()
	registerProxy(t, registry, &proxyStub{})
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(context.Background())
	value, err := host.Resolve(ports.UpdaterName)
	require.NoError(t, err)
	service := value.(*Service)
	call, done, err := service.begin(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	require.ErrorIs(t, call.Err(), context.Canceled)
	done()
	require.NoError(t, host.Stop(context.Background()))
	_, err = service.Check(context.Background())
	require.ErrorContains(t, err, "stopped")
}

type proxyStub struct {
	err   error
	calls int
}

func (p *proxyStub) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.NetworkProxyName, p)
}
func (*proxyStub) Start(context.Context) error                    { return nil }
func (*proxyStub) Stop(context.Context) error                     { return nil }
func (*proxyStub) Reconfigure(context.Context, config.View) error { return nil }
func (p *proxyStub) Proxy(context.Context) (string, error)        { p.calls++; return "", p.err }
func registerProxy(t *testing.T, registry *rte.Registry, proxy *proxyStub) {
	t.Helper()
	require.NoError(t, registry.Register(manifest.Manifest{ID: "test.proxy", Provides: []manifest.Port{manifest.PortOf[ports.NetworkProxy](ports.NetworkProxyName, 1, 0)}}, func() rte.Component { return proxy }))
}

func TestUpdaterUsesSharedProxyAndDoesNotFallBackToLegacyOverride(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	want := errors.New("shared proxy unavailable")
	proxy := &proxyStub{err: want}
	registerProxy(t, registry, proxy)
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {"proxy": "http://obsolete:secret@127.0.0.1:1"}})
	require.NoError(t, err)
	host.Start(ctx)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	value, err := host.Resolve(ports.UpdaterName)
	require.NoError(t, err)
	service := value.(ports.Updater)
	_, err = service.Check(ctx)
	require.ErrorIs(t, err, want)
	_, _, err = service.Download(ctx)
	require.ErrorIs(t, err, want)
	require.Equal(t, 2, proxy.calls)
}
