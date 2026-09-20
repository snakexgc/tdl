// Component bindings project validated settings into transport snapshots.
package config

type binding struct {
	Component   string
	EnabledPath string
	Fields      map[string]string
}

func bindings() []binding {
	return []binding{
		{accountComponentID, "", map[string]string{"api_id": "telegram.api_id", "api_hash": "telegram.api_hash", "builtin_preset": "telegram.builtin_preset", "use_builtin": "telegram.use_builtin", proxyField: proxyField, ntpField: ntpField, fileLimitField: "limit", poolSizeField: "pool_size", "delay_seconds": "delay", reconnectTimeoutField: "reconnect_timeout"}},
		{consoleComponentID, "modules.bot", map[string]string{"token": "bot.token", "allowed_users": "bot.allowed_users"}},
		{"notify.telegram", "", map[string]string{"on_download_start": "bot.notify.on_download_start", "on_download_complete": "bot.notify.on_download_complete", "on_download_pause": "bot.notify.on_download_pause", "on_download_error": "bot.notify.on_download_error", "live_progress": "bot.notify.live_progress", "live_progress_interval_seconds": "bot.notify.live_progress_interval_seconds"}},
		{"filter.rules", "", map[string]string{"include": "include", "exclude": "exclude", "min_mb": "file_size_min_mb", "max_mb": "file_size_max_mb"}},
		{"naming.rules", "", map[string]string{"filename": "filename", directoryField: "download_dir", "max_bytes": "filename_max_length"}},
		{"trigger.reaction", "", map[string]string{"download": "trigger_reactions", "forward": "forward.trigger_reactions"}},
		{"trigger.download", "modules.watch", nil},
		{forwardTriggerComponentID, "modules.forward", map[string]string{"listen": "forward.listen", "listen_comments": "forward.listen_comments"}},
		{"downloader.aria2", "modules.aria2", map[string]string{"rpc_url": "aria2.rpc_url", "secret": "aria2.secret", directoryField: "aria2.dir", "timeout_seconds": "aria2.timeout_seconds", "auto_download": "aria2.auto_download"}},
		{downloadComponentID, "", map[string]string{"executors": "downloader.executors", "local_root": "downloader.local_root"}},
		{proxyComponentID, "modules.http", map[string]string{"address": "http.address", portField: "http.port", "public_base_url": "http.public_base_url", "link_ttl_hours": "http.download_link_ttl_hours"}},
		{panelComponentID, "modules.webui", map[string]string{"address": "webui.address", portField: "webui.port", "username": "webui.username", "password": "webui.password"}},
		{forwardComponentID, "", map[string]string{modeField: "forward.mode", "target": "forward.target", "silent": "forward.silent", "dedupe_ttl_seconds": "forward.dedupe_ttl_seconds"}},
	}
}

const proxyField = "proxy"

const (
	accountComponentID = "account.telegram"
	consoleComponentID = "console.bot"
	directoryField     = "directory"
	portField          = "port"
	ntpField           = "ntp"
)

const (
	poolSizeField             = "dc_pool_size"
	forwardTriggerComponentID = "trigger.forward"
	downloadComponentID       = "download.control"
	proxyComponentID          = "proxy.range"
	panelComponentID          = "panel.webui"
	forwardComponentID        = "forwarder"
	modeField                 = "mode"
	reconnectTimeoutField     = "reconnect_timeout_seconds"
)

const fileLimitField = "file_limit"
