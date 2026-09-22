// Package timesync owns periodic clock calibration and the process time port.
package timesync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	serverField   = "server"
	intervalField = "sync_interval_seconds"
	timeoutField  = "timeout_seconds"
)

type policy struct {
	server            string
	interval, timeout time.Duration
}

func Manifest() manifest.Manifest {
	return manifest.WithSettings(manifest.Manifest{
		ID: ID, Title: "时间同步", Feature: manifest.Feature{ID: "time", Title: "时间同步", Order: 75, SettingsURL: "/config?tab=network"},
		Provides: []manifest.Port{manifest.PortOf[ports.Clock](ports.ClockName, 1, 0)},
		Config: []manifest.ConfigField{
			manifest.Text(serverField, "首选 NTP 服务器", "", false, true).WithHelp("优先使用此服务器；留空或不可达时从内置候选中选择。同步结果只更新运行状态，不改写此配置。"),
			manifest.Number(intervalField, "同步间隔（秒）", 300, 60, 86400, true).WithHelp("默认每 5 分钟同步，可设为 60 秒。首次启动立即在后台同步；失败保留上次成功的时间偏移，下个周期重试。"),
			manifest.Number(timeoutField, "单次探测超时（秒）", 3, 1, 30, true),
		},
	}, "network", "时间同步").SettingsOrder(40)
}

func Register(registry *rte.Registry, probe ports.TimeProbe) error {
	return registry.Register(Manifest(), func() rte.Component { return New(probe) })
}

type Service struct {
	probe         ports.TimeProbe
	configuration atomic.Pointer[policy]
	status        atomic.Pointer[types.ClockStatus]
	runnables     *schedule.Group
	changed       chan struct{}
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
	var server string
	var interval, timeout int64
	for name, value := range map[string]any{serverField: &server, intervalField: &interval, timeoutField: &timeout} {
		if err := view.Get(name, value); err != nil {
			return nil, err
		}
	}
	server = strings.TrimSpace(server)
	if strings.ContainsAny(server, " \r\n\t/\\") {
		return nil, fmt.Errorf("server must be a hostname or IP address, optionally with a port")
	}
	next := &policy{server: server, interval: time.Duration(interval) * time.Second, timeout: time.Duration(timeout) * time.Second}
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

func (s *Service) Now() time.Time { return time.Now().Add(s.Status().Offset) }
func (s *Service) Status() types.ClockStatus {
	if status := s.status.Load(); status != nil {
		return *status
	}
	return types.ClockStatus{}
}

func (s *Service) synchronize(ctx context.Context) error {
	p := s.configuration.Load()
	server, sample, err := selectServer(ctx, p.server, builtinServers, s.probe, p.timeout)
	if ctx.Err() != nil {
		return nil // Normal shutdown must not overwrite the last calibration.
	}
	status := s.Status()
	status.LastAttempt = time.Now()
	if err != nil {
		status.LastError = err.Error()
		s.status.Store(&status)
		slog.Warn("NTP 同步失败，将在下个周期重试", "component", ID, "synchronized", status.Synchronized, "error", err)
		return err
	}
	status.Server, status.Offset, status.Synchronized = server, sample.Offset, true
	status.LastSuccess, status.LastError = time.Now(), ""
	s.status.Store(&status)
	slog.Info("NTP 时间已同步", "component", ID, serverField, server, "offset", sample.Offset, "elapsed", sample.Elapsed)
	return nil
}
