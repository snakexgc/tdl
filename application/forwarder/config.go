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
			manifest.Choice("mode", "默认转发模式", forwardModeDefault, []string{forwardModeDefault, forwardModeClone}, false).WithHelp("default 优先官方转发，失败时降级为复制；clone 始终复制发送。"),
			manifest.Text("target", "默认目标", "", false, false).WithHelp("未指定目标时使用；留空表示收藏夹。已匹配规则的来源使用规则目标。"),
			manifest.Flag("silent", "静默发送", false, false),
			manifest.Number("dedupe_ttl_seconds", "去重有效期（秒）", 600, 1, 8640000, false),

			field("poll_interval_ms", "队列扫描间隔（毫秒）", 2000, 100, 3600000),
			field(retryBaseField, "首次重试间隔（秒）", 5, 1, 86400),
			field("retry_max_seconds", "最大重试间隔（秒）", 300, 1, 86400),
			field("max_attempts", "失败次数上限", 10, 1, 100),
			field("history_hours", "已结束任务保留时间（小时）", 24, 1, 8760),
			field("history_limit", "已结束任务保留数量", 200, 1, 100000),
		},
	}, "forward", "默认转发与队列", "dedupe_ttl_seconds", "poll_interval_ms", "retry_base_seconds", "retry_max_seconds", "max_attempts", "history_hours", "history_limit")
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
