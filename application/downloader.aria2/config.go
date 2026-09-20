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
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "download", Title: "下载管理", Order: 10, SettingsURL: "/config?tab=download"}, ID: ID, Title: "aria2 下载器", Pages: []manifest.Page{{Path: "/aria2ng.html", Title: "高级下载控制（AriaNg）", NavHidden: true}}, Provides: []manifest.Port{
			manifest.PortOf[ports.DownloadExecutor](ports.DownloadExecutorName, 1, 0),
			manifest.PortOf[ports.Aria2Tasks](ports.Aria2TasksName, 1, 0),
		}, Config: []manifest.ConfigField{
			manifest.Flag("auto_download", "自动提交到 aria2", true, false).WithHelp("使用 aria2 下载方式时需要开启；关闭后不再自动向 aria2 提交新任务。"),
			manifest.FormattedText("rpc_url", "aria2 RPC 地址", "http://127.0.0.1:6800/jsonrpc", "url", true, true).WithHelp("显示配置文件中的地址，认证信息不回显。填写 TDL 能访问的完整 RPC 地址，例如 http://127.0.0.1:6800/jsonrpc；留空保留原值。容器中的 127.0.0.1 指容器自身。"),
			manifest.Text("secret", "aria2 RPC 密钥", "", true, true).WithHelp("与 aria2 的 rpc-secret 一致。留空保留原值。"),
			manifest.Text("directory", "aria2 保存目录", "", false, false).WithHelp("填写 aria2 所在机器上的目录；留空使用 aria2 自身的默认下载目录。"),
			manifest.Number("timeout_seconds", "连接超时（秒）", 30, 1, 3600, true).WithHelp("单次 aria2 RPC 请求的最长等待时间。"),

			field("status_interval_ms", "状态同步间隔（毫秒）", 60000, 100, 3600000).WithHelp("向 aria2 查询任务状态的间隔；1000 毫秒 = 1 秒。"),
			field("connect_retry_ms", "连接首次重试间隔（毫秒）", 10000, 100, 3600000).WithHelp("连接失败后从此间隔开始重试，不能大于最大重试间隔。"),
			field("connect_retry_max_ms", "连接最大重试间隔（毫秒）", 60000, 100, 3600000).WithHelp("连续连接失败时，重试等待时间增长的上限。"),
			field(monitorPollField, "无速度检查间隔（毫秒）", 30000, 100, 3600000).WithHelp("检查下载中任务是否长时间没有速度的频率。"),
			field("monitor_stall_seconds", "无速度持续时间（秒）", 180, 1, 86400).WithHelp("任务持续没有速度达到此时间后，尝试暂停再恢复。"),
			field("monitor_pause_seconds", "无速度恢复等待（秒）", 10, 1, 3600).WithHelp("恢复无速度任务时，暂停后等待多久再继续下载。"),
			field("monitor_action_seconds", "无速度恢复操作超时（秒）", 30, 1, 300).WithHelp("执行暂停或恢复操作时的最长等待时间。"),
			field("error_window_seconds", "Telegram 错误统计时段（秒）", 10, 1, 3600).WithHelp("在最近这段时间内累计 Telegram 下载错误次数。"),
			field("error_threshold", "触发恢复的错误次数", 3, 1, 10000).WithHelp("统计时段内错误达到此次数后，尝试暂停再恢复下载。"),
			field("error_cooldown_seconds", "错误恢复冷却时间（秒）", 10, 1, 3600).WithHelp("两次错误恢复操作之间的最短间隔，避免频繁操作。"),
			field("error_pause_seconds", "错误恢复等待（秒）", 5, 1, 3600).WithHelp("因 Telegram 错误暂停下载后，等待多久再恢复。"),
			field("error_action_seconds", "错误恢复操作超时（秒）", 30, 1, 300).WithHelp("因 Telegram 错误执行暂停或恢复操作时的最长等待时间。"),
		},
	}, "download", "aria2 连接", "status_interval_ms", "connect_retry_ms", "connect_retry_max_ms", "monitor_poll_ms", "monitor_stall_seconds", "monitor_pause_seconds", "monitor_action_seconds", "error_window_seconds", "error_threshold", "error_cooldown_seconds", "error_pause_seconds", "error_action_seconds").SettingsOrder(20)
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
		if field.RestartRequired || field.Type != manifest.Int {
			continue
		}
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

// ValidateConfiguration validates offline edits without acquiring resources.
func ValidateConfiguration(ctx context.Context, view config.View) error {
	_, err := (&service{}).PrepareConfig(ctx, view)
	return err
}
