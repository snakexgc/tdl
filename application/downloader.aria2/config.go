package aria2

import (
	"context"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/config"
)

const monitorPollField = "monitor_poll_ms"

type governancePolicy struct {
	status, retry, maximum time.Duration
	monitor                zeroSpeedMonitorConfig
	regulator              telegramErrorRegulatorConfig
}

func Manifest() manifest.Manifest {
	field := func(name, title string, value, low, high int64) manifest.ConfigField {
		return manifest.ConfigField{Name: name, Title: title, Type: manifest.Int, Default: value, Min: &low, Max: &high}
	}
	return manifest.Manifest{ID: ID, Title: "aria2 下载器", Pages: []manifest.Page{{Path: "/aria2ng.html", Title: "AriaNg"}}, Provides: []manifest.Port{
		manifest.PortOf[ports.DownloadExecutor](ports.DownloadExecutorName, 1, 0),
		manifest.PortOf[ports.Aria2Tasks](ports.Aria2TasksName, 1, 0),
	}, Config: []manifest.ConfigField{
		field("status_interval_ms", "状态同步间隔（毫秒）", 60000, 100, 3600000),
		field("connect_retry_ms", "连接首次重试间隔（毫秒）", 10000, 100, 3600000),
		field("connect_retry_max_ms", "连接最大重试间隔（毫秒）", 60000, 100, 3600000),
		field(monitorPollField, "零速检测间隔（毫秒）", 30000, 100, 3600000),
		field("monitor_stall_seconds", "零速持续阈值（秒）", 180, 1, 86400),
		field("monitor_pause_seconds", "零速恢复暂停时间（秒）", 10, 1, 3600),
		field("monitor_action_seconds", "零速治理操作超时（秒）", 30, 1, 300),
		field("error_window_seconds", "Telegram 错误统计窗口（秒）", 10, 1, 3600),
		field("error_threshold", "Telegram 错误触发次数", 3, 1, 10000),
		field("error_cooldown_seconds", "Telegram 错误治理冷却时间（秒）", 10, 1, 3600),
		field("error_pause_seconds", "Telegram 错误恢复暂停时间（秒）", 5, 1, 3600),
		field("error_action_seconds", "Telegram 错误治理操作超时（秒）", 30, 1, 300),
	}}
}

func (m *Manager) policy() governancePolicy {
	if p := m.configuration.Load(); p != nil {
		return *p
	}
	return governancePolicy{time.Minute, DefaultConnectRetryInterval, maxConnectRetryInterval, m.monitor.cfg, m.regulator.cfg}
}

func (s *service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := make(map[string]int64)
	for _, field := range Manifest().Config {
		var value int64
		if err := view.Get(field.Name, &value); err != nil {
			return nil, err
		}
		values[field.Name] = value
	}
	if values["connect_retry_ms"] > values["connect_retry_max_ms"] {
		return nil, fmt.Errorf("connect_retry_ms must not exceed connect_retry_max_ms")
	}
	p := governancePolicy{
		status:    time.Duration(values["status_interval_ms"]) * time.Millisecond,
		retry:     time.Duration(values["connect_retry_ms"]) * time.Millisecond,
		maximum:   time.Duration(values["connect_retry_max_ms"]) * time.Millisecond,
		monitor:   zeroSpeedMonitorConfig{time.Duration(values[monitorPollField]) * time.Millisecond, time.Duration(values["monitor_stall_seconds"]) * time.Second, time.Duration(values["monitor_pause_seconds"]) * time.Second, time.Duration(values["monitor_action_seconds"]) * time.Second},
		regulator: telegramErrorRegulatorConfig{Window: time.Duration(values["error_window_seconds"]) * time.Second, Threshold: int(values["error_threshold"]), Cooldown: time.Duration(values["error_cooldown_seconds"]) * time.Second, PauseDuration: time.Duration(values["error_pause_seconds"]) * time.Second, ActionTimeout: time.Duration(values["error_action_seconds"]) * time.Second, EventBuffer: defaultTelegramErrorRegulatorEventBuffer},
	}
	return func() {
		s.manager.configuration.Store(&p)
		for _, wake := range []chan struct{}{s.manager.statusChanged, s.manager.retryChanged, s.manager.monitor.changed} {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}, nil
}

func (s *service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}
