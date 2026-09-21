package accounttelegram

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	ID                 = "account.telegram"
	fieldAPIID         = "api_id"
	fieldAPIHash       = "api_hash"
	fieldBuiltinPreset = "builtin_preset"
	fieldUseBuiltin    = "use_builtin"
	presetBuiltin      = "builtin"
	presetDesktop      = "desktop"
)

func Manifest() manifest.Manifest {
	zero := int64(0)
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "account", Title: "账号管理", Order: 40, SettingsURL: "/config?tab=account"},
		ID:      ID, Commands: Commands(), Title: "Telegram 账号",
		Pages:    []manifest.Page{{Path: "/user", Title: "账号管理", View: "user", Module: "/static/js/user.js", Style: "/static/css/user.css", Order: 40, KeepVisible: true, SettingsURL: "/config?tab=account"}},
		Provides: []manifest.Port{manifest.PortOf[ports.TelegramCredentials](ports.TelegramCredentialsName, 1, 0), manifest.PortOf[ports.NetworkProxy](ports.NetworkProxyName, 1, 0)},
		Config: []manifest.ConfigField{
			manifest.FormattedText("proxy", "统一网络代理", "", "proxy", true, true).InSettings("network", "网络代理与连接").WithHelp("Telegram、机器人和软件更新共用此代理。选择协议后填写 IP 或域名加端口，例如 127.0.0.1:1080；需要认证时展开填写。整体留空保留已保存的代理，修改时请同时填写所需的认证信息。"),
			manifest.Number("file_limit", "同时下载的文件数", 1, 1, 10000, false).InSettings("download", "下载并发与节奏").WithHelp("限制同时下载的 Telegram 文件数。调大前请确认网络带宽和账号连接稳定。"),
			manifest.Number("dc_pool_size", "每个数据中心的连接数", 8, 1, 10000, false).InSettings("download", "下载并发与节奏").WithHelp("每个 Telegram 数据中心的下载连接容量及分片请求并发上限。HTTP 多线程请求与本地下载器共享此额度，超出时排队；HTTP 直读已完成的本地文件不受此限制。"),
			manifest.Number("delay_seconds", "任务间隔（秒）", 0, 0, 3600, true).InSettings("download", "下载并发与节奏").WithHelp("任务之间的等待时间；0 表示不额外等待。"),
			manifest.Number("reconnect_timeout_seconds", "断线重试间隔（秒）", 3, 1, 86400, true).InSettings("network", "网络代理与连接").WithHelp("Telegram 监听连接中断后，等待这些秒再重新连接。"),

			manifest.Flag(fieldUseBuiltin, "使用内置 API 凭据", true, false).WithHelp("通常保持开启，无需申请 API 凭据。使用自己的 API ID 和 API Hash 时关闭此项，并成对填写下方凭据。"),
			{Name: fieldAPIID, Title: "自定义 API ID", Type: manifest.Int, Default: 0, Min: &zero, EmptyPreserves: true, Help: "从 my.telegram.org 获取，与 API Hash 配套使用。仅在关闭「使用内置 API 凭据」后优先使用；留空保留原值。"},
			{Name: fieldAPIHash, Title: "API Hash", Type: manifest.String, Default: "", Secret: true, Help: "留空保持不变。填写自定义 API ID 时需同时提供对应的 API Hash。"},
			{Name: fieldBuiltinPreset, Title: "内置预设", Type: manifest.String, Default: presetDesktop, Choices: []string{presetDesktop, presetBuiltin}, ChoiceLabels: map[string]string{presetDesktop: "desktop（推荐）", presetBuiltin: presetBuiltin}, Help: "默认 desktop，不建议修改。更换预设可能需要重新登录。"},
		},
	}, "account", "Telegram API 凭据", "reconnect_timeout_seconds", "dc_pool_size", fieldBuiltinPreset).SettingsOrder(30)
}

func Register(registry *rte.Registry) error {
	return registry.Register(Manifest(), func() rte.Component { return &Credentials{} })
}

type Credentials struct {
	proxy    atomic.Pointer[string]
	account  types.AccountID
	settings atomic.Pointer[types.TelegramCredentialsConfig]
}

func (c *Credentials) Init(ctx context.Context, k rte.Kernel) error {
	c.account = k.Account
	if err := c.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	if err := k.Provide(ports.NetworkProxyName, c); err != nil {
		return err
	}
	return k.Provide(ports.TelegramCredentialsName, c)
}
func (*Credentials) Start(context.Context) error { return nil }
func (*Credentials) Stop(context.Context) error  { return nil }
func (c *Credentials) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := c.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (c *Credentials) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var proxy string
	if err := view.Get("proxy", &proxy); err != nil {
		return nil, err
	}
	var settings types.TelegramCredentialsConfig
	for _, field := range []struct {
		name   string
		target any
	}{
		{fieldAPIID, &settings.APIID},
		{fieldAPIHash, &settings.APIHash},
		{fieldBuiltinPreset, &settings.BuiltinPreset},
		{fieldUseBuiltin, &settings.UseBuiltin},
	} {
		if err := view.Get(field.name, field.target); err != nil {
			return nil, err
		}
	}
	settings.APIHash = strings.TrimSpace(settings.APIHash)
	settings.BuiltinPreset = strings.TrimSpace(settings.BuiltinPreset)
	if err := Validate(settings); err != nil {
		return nil, err
	}
	return func() { c.settings.Store(&settings); c.proxy.Store(&proxy) }, nil
}

func (c *Credentials) Proxy(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value := c.proxy.Load()
	if value == nil {
		return "", fmt.Errorf("network proxy is not initialized")
	}
	return *value, nil
}

func Validate(settings types.TelegramCredentialsConfig) error {
	if settings.APIID < 0 || (settings.APIID == 0) != (strings.TrimSpace(settings.APIHash) == "") {
		return fmt.Errorf("api_id and api_hash must both be provided, or both left empty")
	}
	if _, err := Preset(settings.BuiltinPreset); err != nil {
		return err
	}
	return nil
}

func (c *Credentials) Resolve(ctx context.Context, account types.AccountID) (types.TelegramCredentials, error) {
	if err := ctx.Err(); err != nil {
		return types.TelegramCredentials{}, err
	}
	if account != c.account {
		return types.TelegramCredentials{}, fmt.Errorf("credential account mismatch")
	}
	settings := c.settings.Load()
	if settings == nil {
		return types.TelegramCredentials{}, fmt.Errorf("credential component is not initialized")
	}
	preset := settings.BuiltinPreset
	app, err := Preset(preset)
	if err != nil {
		return types.TelegramCredentials{}, err
	}
	if settings.APIID > 0 && !settings.UseBuiltin {
		app = types.TelegramApp{AppID: settings.APIID, AppHash: settings.APIHash}
	}
	return types.TelegramCredentials{App: app, Preset: preset}, nil
}

func Preset(name string) (types.TelegramApp, error) {
	switch name {
	case presetBuiltin:
		return types.TelegramApp{AppID: 15055931, AppHash: "021d433426cbb920eeb95164498fe3d3"}, nil
	case presetDesktop:
		return types.TelegramApp{AppID: 2040, AppHash: "b18441a1ff607e10a989891a5462e627"}, nil
	default:
		return types.TelegramApp{}, fmt.Errorf("unknown Telegram application preset %q", name)
	}
}
