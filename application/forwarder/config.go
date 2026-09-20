package forwarder

import (
	"context"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/config"
)

const retryBaseField = "retry_base_seconds"

type policy struct {
	poll, base, maximum, retention time.Duration
	attempts, history              int
	command                        CommandSettings
	dedupe                         time.Duration
}

func defaultPolicy() policy {
	return policy{pollInterval, backoffBase, backoffMax, terminalTTL, maxAttempts, maxTerminal, CommandSettings{Mode: forwardModeDefault}, 10 * time.Minute}
}

func (q *Queue) policy() policy {
	if current := q.configuration.Load(); current != nil {
		return *current
	}
	return defaultPolicy()
}

func Manifest() manifest.Manifest {
	field := func(name, title string, value, low, high int64) manifest.ConfigField {
		return manifest.ConfigField{Name: name, Title: title, Type: manifest.Int, Default: value, Min: &low, Max: &high}
	}
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "forward", Title: "转发管理", Order: 20, SettingsURL: "/config?tab=forward"},
		ID:      ID, Commands: Commands(), Title: "转发队列", Pages: []manifest.Page{{Path: "/forwards", Title: "转发管理", View: "forwards", Module: "/static/js/forwards.js", Style: "/static/css/forwards.css", Order: 30, KeepVisible: true, SettingsURL: "/config?tab=forward"}},
		Provides: []manifest.Port{manifest.PortOf[ports.ForwardTasks](ports.ForwardTasksName, 1, 0), manifest.PortOf[ports.ConsoleCommandHandler](commandPort, 1, 0), manifest.PortOf[ports.ForwardRouting](ports.ForwardRoutingName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: "mode", Title: "默认转发方式", Type: manifest.String, Default: forwardModeDefault, Choices: []string{forwardModeDefault, forwardModeClone}, ChoiceLabels: map[string]string{forwardModeDefault: "优先转发，失败时复制", forwardModeClone: "始终复制发送"}, Help: "转发保留消息来源，复制以当前账号重新发送。匹配规则时使用规则指定的方式。"},
			manifest.Text("target", "默认目标", "", false, false).WithHelp("未指定目标时使用；留空表示收藏夹。已匹配规则的来源使用规则目标。"),
			manifest.Flag("silent", "静默发送", false, false).WithHelp("发送时不触发接收方的声音通知；匹配规则时使用规则指定的选项。"),
			manifest.Number("dedupe_ttl_seconds", "重复任务忽略时长（秒）", 600, 1, 8640000, false).WithHelp("在此时间内忽略重复提交的同一转发任务。"),

			field("poll_interval_ms", "任务检查间隔（毫秒）", 2000, 100, 3600000).WithHelp("检查等待执行或等待重试的转发任务的间隔。"),
			field(retryBaseField, "首次重试间隔（秒）", 5, 1, 86400).WithHelp("转发失败后从此间隔开始重试，不能大于最大重试间隔。"),
			field("retry_max_seconds", "最大重试间隔（秒）", 300, 1, 86400).WithHelp("连续失败时，重试等待时间增长的上限。"),
			field("max_attempts", "失败次数上限", 10, 1, 100).WithHelp("累计失败达到此次数后停止自动重试。"),
			field("history_hours", "历史任务保留时间（小时）", 24, 1, 8760).WithHelp("已结束任务超过此时间后清理，不删除 Telegram 消息。"),
			field("history_limit", "历史任务保留数量", 200, 1, 100000).WithHelp("最多保留这些已结束任务；超出时优先清理较早的记录。"),
		},
	}, "forward", "默认转发与重试", "dedupe_ttl_seconds", "poll_interval_ms", "retry_base_seconds", "retry_max_seconds", "max_attempts", "history_hours", "history_limit").SettingsOrder(20)
}

func (s *service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var poll, base, maximum, attempts, retention, history int64
	for name, target := range map[string]*int64{
		"poll_interval_ms": &poll, retryBaseField: &base,
		"retry_max_seconds": &maximum, "max_attempts": &attempts, "history_hours": &retention, "history_limit": &history,
	} {
		if err := view.Get(name, target); err != nil {
			return nil, err
		}
	}
	if base > maximum {
		return nil, fmt.Errorf("retry_base_seconds must not exceed retry_max_seconds")
	}
	var command CommandSettings
	if err := view.Get("target", &command.Target); err != nil {
		return nil, err
	}
	if err := view.Get("mode", &command.Mode); err != nil {
		return nil, err
	}
	if err := view.Get("silent", &command.Silent); err != nil {
		return nil, err
	}
	var dedupe int64
	if err := view.Get("dedupe_ttl_seconds", &dedupe); err != nil {
		return nil, err
	}
	if dedupe <= 0 {
		return nil, fmt.Errorf("dedupe_ttl_seconds must be positive")
	}
	p := policy{
		time.Duration(poll) * time.Millisecond, time.Duration(base) * time.Second, time.Duration(maximum) * time.Second,
		time.Duration(retention) * time.Hour, int(attempts), int(history), command, time.Duration(dedupe) * time.Second,
	}
	return func() { s.queue.configuration.Store(&p); s.queue.signal() }, nil
}

func (s *service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (p policy) backoff(attempts int) time.Duration {
	delay := p.base
	for attempt := 1; attempt < attempts; attempt++ {
		if delay >= p.maximum/2 {
			return p.maximum
		}
		delay *= 2
	}
	if delay > p.maximum {
		return p.maximum
	}
	return delay
}

// ValidateConfiguration validates offline edits without acquiring resources.
func ValidateConfiguration(ctx context.Context, view config.View) error {
	_, err := (&service{}).PrepareConfig(ctx, view)
	return err
}
