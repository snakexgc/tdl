package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/config"
)

const clientWaitField = "client_wait_seconds"

type policy struct{ wait, persist, tasks, sources time.Duration }

func Manifest() manifest.Manifest {
	field := func(name, title string, value, high int64) manifest.ConfigField {
		low := int64(1)
		return manifest.ConfigField{Name: name, Title: title, Type: manifest.Int, Default: value, Min: &low, Max: &high}
	}
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "links", Title: "下载链接服务", Order: 30, SettingsURL: "/config?tab=links"}, ID: ID, Title: "HTTP Range 代理", Provides: []manifest.Port{manifest.PortOf[http.Handler](ports.RangeHandlerName, 1, 0)}, Config: []manifest.ConfigField{
			manifest.Text("address", "监听地址", "0.0.0.0", false, true),
			manifest.Number("port", "监听端口", 22334, 1, 65535, true),
			manifest.FormattedText("public_base_url", "公开访问地址", "", "url", false, false),
			manifest.Number("link_ttl_hours", "链接有效期（小时）", 24, 0, 876000, false),

			field(clientWaitField, "等待账号连接超时（秒）", 30, 600),
			field("persist_timeout_seconds", "传输记录保存超时（秒）", 5, 300),
			field("task_cleanup_seconds", "过期任务清理间隔（秒）", 3600, 86400),
			field("source_cleanup_seconds", "闲置源清理间隔（秒）", 60, 3600),
		},
	}, "links", "下载链接服务", "client_wait_seconds", "persist_timeout_seconds", "task_cleanup_seconds", "source_cleanup_seconds")
}

func (h *Handler) policy() policy {
	if current := h.configuration.Load(); current != nil {
		return *current
	}
	wait := h.clientWaitTimeout
	if wait <= 0 {
		wait = telegramClientWaitTimeout
	}
	return policy{wait, httpDeliveryPersistTimeout, time.Hour, time.Minute}
}

func (h *Handler) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var wait, persist, tasks, sources int64
	for name, target := range map[string]*int64{
		clientWaitField: &wait, "persist_timeout_seconds": &persist,
		"task_cleanup_seconds": &tasks, "source_cleanup_seconds": &sources,
	} {
		if err := view.Get(name, target); err != nil {
			return nil, err
		}
	}
	p := policy{time.Duration(wait) * time.Second, time.Duration(persist) * time.Second, time.Duration(tasks) * time.Second, time.Duration(sources) * time.Second}
	return func() {
		h.configuration.Store(&p)
		for _, wake := range []chan struct{}{h.taskChanged, h.sourceChanged} {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}, nil
}

func (h *Handler) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := h.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

// ValidateConfiguration validates offline edits without acquiring resources.
func ValidateConfiguration(ctx context.Context, view config.View) error {
	_, err := (&Handler{}).PrepareConfig(ctx, view)
	return err
}
