package application

import (
	"context"
	"fmt"

	timesync "github.com/snakexgc/tdl/application/time.sync"
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func TimeHost(ctx context.Context, account types.AccountID, store *config.Store, probe ports.TimeProbe, proxy ports.NetworkProxy) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if proxy != nil {
		if err := registry.Register(manifest.Manifest{ID: "host.time.proxy", Provides: []manifest.Port{manifest.PortOf[ports.NetworkProxy](ports.NetworkProxyName, 1, 0)}}, func() rte.Component { return timeProxyBridge{proxy} }); err != nil {
			return nil, err
		}
	}
	if err := timesync.Register(registry, probe); err != nil {
		return nil, err
	}
	values, err := componentValues(ctx, timesync.ID, store)
	if err != nil {
		return nil, err
	}
	host, err := registry.Build(account, nil, values)
	if err != nil {
		return nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	return host, nil
}

// The bridge supplies the policy owner's port without duplicating its config.
type timeProxyBridge struct{ proxy ports.NetworkProxy }

func (p timeProxyBridge) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.NetworkProxyName, p.proxy)
}
func (timeProxyBridge) Start(context.Context) error                    { return nil }
func (timeProxyBridge) Stop(context.Context) error                     { return nil }
func (timeProxyBridge) Reconfigure(context.Context, config.View) error { return nil }
