// Package componentconfig is the compatibility boundary between ECUC documents
// and legacy protocol adapters. Business components never receive this mapping.
package componentconfig

type Binding struct {
	Component   string
	EnabledPath string
	Fields      map[string]string
}

func Bindings() []Binding {
	return []Binding{
		{"account.telegram", "", map[string]string{"api_id": "telegram.api_id", "api_hash": "telegram.api_hash", "builtin_preset": "telegram.builtin_preset", "use_builtin": "telegram.use_builtin", proxyField: proxyField, "ntp": "ntp", "file_limit": "limit", "dc_pool_size": "pool_size", "delay_seconds": "delay", "reconnect_timeout_seconds": "reconnect_timeout"}},
		{"console.bot", "modules.bot", map[string]string{"token": "bot.token", "allowed_users": "bot.allowed_users"}},
		{"notify.telegram", "", map[string]string{"on_download_start": "bot.notify.on_download_start", "on_download_complete": "bot.notify.on_download_complete", "on_download_pause": "bot.notify.on_download_pause", "on_download_error": "bot.notify.on_download_error", "live_progress": "bot.notify.live_progress", "live_progress_interval_seconds": "bot.notify.live_progress_interval_seconds"}},
		{"filter.rules", "", map[string]string{"include": "include", "exclude": "exclude", "min_mb": "file_size_min_mb", "max_mb": "file_size_max_mb"}},
		{"naming.rules", "", map[string]string{"filename": "filename", "directory": "download_dir", "max_bytes": "filename_max_length"}},
		{"trigger.reaction", "", map[string]string{"download": "trigger_reactions", "forward": "forward.trigger_reactions"}},
		{"trigger.download", "modules.watch", nil},
		{"trigger.forward", "modules.forward", map[string]string{"listen": "forward.listen", "listen_comments": "forward.listen_comments"}},
		{"downloader.aria2", "modules.aria2", map[string]string{"rpc_url": "aria2.rpc_url", "secret": "aria2.secret", "directory": "aria2.dir", "timeout_seconds": "aria2.timeout_seconds", "auto_download": "aria2.auto_download"}},
		{"download.control", "", map[string]string{"mode": "downloader.mode", "local_root": "downloader.local_root"}},
		{"proxy.range", "modules.http", map[string]string{"address": "http.address", "port": "http.port", "public_base_url": "http.public_base_url", "link_ttl_hours": "http.download_link_ttl_hours"}},
		{"panel.webui", "modules.webui", map[string]string{"address": "webui.address", "port": "webui.port", "username": "webui.username", "password": "webui.password"}},
		{"forwarder", "", map[string]string{"mode": "forward.mode", "target": "forward.target", "silent": "forward.silent", "dedupe_ttl_seconds": "forward.dedupe_ttl_seconds"}},
	}
}

const proxyField = "proxy"
