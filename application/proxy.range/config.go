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
		Feature: manifest.Feature{ID: "links", Title: "下载链接服务", Order: 30, SettingsURL: "/config?tab=download#setting-proxy.range-public_base_url"}, ID: ID, Title: "HTTP Range 代理", Provides: []manifest.Port{manifest.PortOf[http.Handler](ports.RangeHandlerName, 1, 0)}, Config: []manifest.ConfigField{
			manifest.FormattedText("public_base_url", "下载链接的访问地址", "http://127.0.0.1:22334", "url", false, false).WithHelp("默认使用本机地址 http://127.0.0.1:22334。下载者或 aria2 位于其他机器或容器时，请填写它们能访问的完整地址，例如 https://files.example.com 或 http://192.168.1.10:22334。"),
			manifest.Number("link_ttl_hours", "链接闲置有效期（小时）", 24, 0, 876000, false).WithHelp("从最近一次活动开始计算，超过后清理链接；0 表示不自动过期。"),
			manifest.Text("address", "服务监听地址", "0.0.0.0", false, true).WithHelp("HTTP 服务在所有下载方式下均随程序启动。0.0.0.0 监听所有 IPv4 网卡；127.0.0.1 仅允许本机访问；IPv6 可填写 ::。监听失败会记录错误日志。"),
			manifest.Number("port", "服务监听端口", 22334, 1, 65535, true).WithHelp("下载链接服务的本机端口。使用容器或反向代理时，访问地址需与实际端口映射对应。"),

			field(clientWaitField, "等待账号连接超时（秒）", 30, 600).WithHelp("下载请求到达后，等待 Telegram 账号连接就绪的最长时间。"),
			field("persist_timeout_seconds", "传输记录保存超时（秒）", 5, 300).WithHelp("将下载活动写入任务记录的最长等待时间。"),
			field("task_cleanup_seconds", "过期链接清理间隔（秒）", 3600, 86400).WithHelp("检查并清理已超过闲置有效期的链接记录的间隔。"),
			field("source_cleanup_seconds", "闲置连接清理间隔（秒）", 60, 3600).WithHelp("检查并释放不再使用的下载来源连接的间隔。"),
		},
	}, "download", "下载链接服务", "client_wait_seconds", "persist_timeout_seconds", "task_cleanup_seconds", "source_cleanup_seconds").SettingsOrder(1)
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
