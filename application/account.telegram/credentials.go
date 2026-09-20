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
	ID            = "account.telegram"
	fieldAPIID    = "api_id"
	fieldAPIHash  = "api_hash"
	presetBuiltin = "builtin"
)

func Manifest() manifest.Manifest {
	zero := int64(0)
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "account", Title: "账号管理", Order: 40, SettingsURL: "/config?tab=account"},
		ID:      ID, Commands: Commands(), Title: "Telegram 账号",
		Pages:    []manifest.Page{{Path: "/user", Title: "账号管理", View: "user", Module: "/static/js/user.js", Style: "/static/css/user.css", Order: 40, KeepVisible: true, SettingsURL: "/config?tab=account"}},
		Provides: []manifest.Port{manifest.PortOf[ports.TelegramCredentials](ports.TelegramCredentialsName, 1, 0), manifest.PortOf[ports.NetworkProxy](ports.NetworkProxyName, 1, 0)},
		Config: []manifest.ConfigField{
			manifest.FormattedText("proxy", "统一网络代理", "", "proxy", true, true).InSettings("network", "网络代理").WithHelp("Telegram、机器人和软件更新共用此代理。选择协议后填写 IP 或域名加端口，例如 127.0.0.1:1080；需要认证时展开填写。整体留空保留已保存的代理，修改时请同时填写所需的认证信息。"),
			manifest.Text("ntp", "时间校准服务器", "", false, true),
			manifest.Number("file_limit", "并发下载文件数", 1, 1, 10000, false).InSettings("download", "下载并发与节奏"),
			manifest.Number("dc_pool_size", "每 DC 下载容量", 8, 1, 10000, false).InSettings("download", "下载并发与节奏"),
			manifest.Number("delay_seconds", "任务间隔（秒）", 0, 0, 3600, true).InSettings("download", "下载并发与节奏"),
			manifest.Number("reconnect_timeout_seconds", "重连等待（秒）", 3, 0, 86400, true),

			{Name: fieldAPIID, Title: "API ID", Type: manifest.Int, Default: 0, Min: &zero},
			{Name: fieldAPIHash, Title: "API Hash", Type: manifest.String, Default: "", Secret: true},
			{Name: "builtin_preset", Title: "内置预设", Type: manifest.String, Default: ""},
			{Name: "use_builtin", Title: "使用内置凭据", Type: manifest.Bool, Default: false},
		},
	}, "account", "Telegram 连接与凭据", "ntp", "reconnect_timeout_seconds", "builtin_preset", "use_builtin")
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
		{"builtin_preset", &settings.BuiltinPreset},
		{"use_builtin", &settings.UseBuiltin},
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
	if settings.BuiltinPreset != "" {
		if _, err := Preset(settings.BuiltinPreset); err != nil {
			return err
		}
	}
	return nil
}

func (c *Credentials) Resolve(ctx context.Context, account types.AccountID, legacy string) (types.TelegramCredentials, error) {
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
	if preset == "" {
		preset = legacy
	}
	if preset == "" {
		preset = presetBuiltin
	}
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
	case "desktop":
		return types.TelegramApp{AppID: 2040, AppHash: "b18441a1ff607e10a989891a5462e627"}, nil
	default:
		return types.TelegramApp{}, fmt.Errorf("unknown Telegram application preset %q", name)
	}
}
