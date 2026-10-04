// Package timesync owns periodic clock calibration and the process time port.
package timesync

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/schedule"
)

const (
	ID            = "time.sync"
	intervalField = "sync_interval_seconds"
	timeoutField  = "timeout_seconds"
)

type policy struct {
	interval, timeout time.Duration
}

func Manifest() manifest.Manifest {
	return manifest.WithSettings(manifest.Manifest{
		ID: ID, Title: "时间同步", Feature: manifest.Feature{ID: "time", Title: "时间同步", Order: 75, SettingsURL: "/config?tab=network"},
		Provides: []manifest.Port{manifest.PortOf[ports.Clock](ports.ClockName, 1, 0)},
		Requires: []manifest.Require{{Port: manifest.PortOf[ports.NetworkProxy](ports.NetworkProxyName, 1, 0), Optional: true}},
		Config: []manifest.ConfigField{
			manifest.Number(intervalField, "同步间隔（秒）", 300, 60, 86400, true).WithHelp("并发查询内置 NTP 池，采用最先返回的有效时间。默认每 5 分钟同步，可设为 60 秒，首次启动立即在后台同步。直连全部失败后通过统一代理重试，仍失败则回落到系统时间，下个周期重试。"),
			manifest.Number(timeoutField, "每轮探测超时（秒）", 3, 1, 30, true).WithHelp("直连和代理重试各使用此超时；收到首个有效时间即取消本轮其余请求。"),
		},
	}, "network", "时间同步").SettingsOrder(40)
}

func Register(registry *rte.Registry, probe ports.TimeProbe) error {
	return registry.Register(Manifest(), func() rte.Component { return New(probe) })
}

type Service struct {
	probe         ports.TimeProbe
	proxy         ports.NetworkProxy
	configuration atomic.Pointer[policy]
	state         atomic.Pointer[clockState]
	runnables     *schedule.Group
	changed       chan struct{}
}

type clockState struct {
	status     types.ClockStatus
	anchor     time.Time
	calibrated time.Time
}

func New(probe ports.TimeProbe) *Service {
	return &Service{probe: probe, changed: make(chan struct{}, 1)}
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	if s.probe == nil {
		return fmt.Errorf("time probe is required")
	}
	if err := s.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	s.runnables = k.Runnables
	if value, err := k.Resolve(ports.NetworkProxyName); err == nil {
		s.proxy = value.(ports.NetworkProxy)
	}
	return k.Provide(ports.ClockName, s)
}

func (s *Service) Start(context.Context) error {
	return s.runnables.RunDynamic("synchronize", 0, func() time.Duration { return s.configuration.Load().interval }, s.changed, s.synchronize, nil)
}
func (*Service) Stop(context.Context) error { return nil } // RTE cancels and drains the runnable first.

func (s *Service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var interval, timeout int64
	for name, value := range map[string]any{intervalField: &interval, timeoutField: &timeout} {
		if err := view.Get(name, value); err != nil {
			return nil, err
		}
	}
	next := &policy{interval: time.Duration(interval) * time.Second, timeout: time.Duration(timeout) * time.Second}
	return func() {
		previous := s.configuration.Swap(next)
		if previous != nil && s.changed != nil {
			select {
			case s.changed <- struct{}{}:
			default:
			}
		}
	}, nil
}

func (s *Service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (s *Service) Now() time.Time {
	if state := s.state.Load(); state != nil && state.status.Synchronized {
		return state.calibrated.Add(time.Since(state.anchor))
	}
	return time.Now()
}

func (s *Service) Status() types.ClockStatus {
	if state := s.state.Load(); state != nil {
		return state.status
	}
	return types.ClockStatus{Source: types.ClockSourceSystem}
}

type proxyProbe struct {
	probe ports.ProxyTimeProbe
	url   string
}

func (p proxyProbe) Query(ctx context.Context, host string, timeout time.Duration) (types.TimeSample, error) {
	return p.probe.QueryViaProxy(ctx, host, p.url, timeout)
}

func (s *Service) synchronize(ctx context.Context) error {
	p := s.configuration.Load()
	server, sample, err := selectServer(ctx, builtinServers, s.probe, p.timeout)
	viaProxy := false
	if err != nil && ctx.Err() == nil && s.proxy != nil {
		proxyURL, proxyErr := s.proxy.Proxy(ctx)
		if proxyErr != nil {
			err = fmt.Errorf("direct time query failed; shared proxy unavailable")
		} else if proxyURL != "" {
			if probe, ok := s.probe.(ports.ProxyTimeProbe); ok {
				server, sample, err = selectServer(ctx, builtinServers, proxyProbe{probe, proxyURL}, p.timeout)
				viaProxy = true
				if err != nil {
					// Proxy errors may contain URLs and authentication details.
					err = fmt.Errorf("direct and proxied time queries failed")
				}
			} else {
				err = fmt.Errorf("direct time query failed; time probe does not support proxy retry")
			}
		}
	}
	if ctx.Err() != nil {
		return nil // Normal shutdown must not overwrite the last calibration.
	}
	status := s.Status()
	status.LastAttempt = time.Now()
	if err != nil {
		status.Server, status.Source, status.Offset = "", types.ClockSourceSystem, 0
		status.Synchronized, status.ViaProxy = false, false
		status.LastError = err.Error()
		s.state.Store(&clockState{status: status})
		slog.Warn("NTP 同步失败，使用系统时间并在下个周期重试", "component", ID, "source", status.Source, "error", err)
		return err
	}
	anchor := sample.MeasuredAt
	if anchor.IsZero() {
		anchor = time.Now()
	}
	calibrated := anchor.Add(sample.Offset).Round(0)
	if sample.Source == "" {
		sample.Source = types.ClockSourceNTP
	}
	if sample.Server != "" {
		server = sample.Server
	}
	status.Server, status.Offset, status.Synchronized = server, sample.Offset, true
	status.Source, status.ViaProxy = sample.Source, viaProxy
	status.LastSuccess, status.LastError = calibrated.Add(time.Since(anchor)), ""
	status.LastAttempt = status.LastSuccess
	s.state.Store(&clockState{status: status, anchor: anchor, calibrated: calibrated})
	slog.Info("NTP 时间已同步", "component", ID, "server", server, "source", sample.Source, "via_proxy", viaProxy, "offset", sample.Offset, "elapsed", sample.Elapsed)
	return nil
}
