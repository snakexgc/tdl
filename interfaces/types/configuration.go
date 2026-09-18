package types

import "encoding/json"

// BotNotifyConfig controls which aria2 events trigger Telegram notifications.
type BotNotifyConfig struct {
	OnDownloadStart         bool `json:"on_download_start"`
	OnDownloadComplete      bool `json:"on_download_complete"`
	OnDownloadPause         bool `json:"on_download_pause"`
	OnDownloadError         bool `json:"on_download_error"`
	LiveProgress            bool `json:"live_progress"`
	LiveProgressIntervalSec int  `json:"live_progress_interval_seconds"`
}

// BotConfig Bot 配置
type BotConfig struct {
	Proxy        string          `json:"proxy,omitempty"`
	Token        string          `json:"token"`
	AllowedUsers []int64         `json:"allowed_users"`
	Notify       BotNotifyConfig `json:"notify"`
}

type HTTPConfig struct {
	Listen               string `json:"listen,omitempty"`
	Address              string `json:"address"`
	Port                 int    `json:"port"`
	PublicBaseURL        string `json:"public_base_url"`
	DownloadLinkTTLHours int    `json:"download_link_ttl_hours"`
}

type WebUIConfig struct {
	Listen   string `json:"listen,omitempty"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ModulesConfig struct {
	WebUI   bool `json:"webui"`
	Bot     bool `json:"bot"`
	Watch   bool `json:"watch"`
	HTTP    bool `json:"http"`
	Aria2   bool `json:"aria2"`
	Forward bool `json:"forward"`
}

type DownloaderConfig struct {
	Mode string `json:"mode"`
}

type ForwardConfig struct {
	Mode             string   `json:"mode"`
	Target           string   `json:"target"`
	Listen           []string `json:"listen"`
	ListenComments   bool     `json:"listen_comments"`
	Silent           bool     `json:"silent"`
	DedupeTTLSeconds int      `json:"dedupe_ttl_seconds"`
	TriggerReactions []string `json:"trigger_reactions"`
}

// Config 全局配置结构
type RuntimeConfig struct {
	Telegram         TelegramCredentialsConfig `json:"telegram"`
	Proxy            string                    `json:"proxy"`
	ProxyUsername    string                    `json:"proxy_username"`
	ProxyPassword    string                    `json:"proxy_password"`
	Namespace        string                    `json:"namespace"`
	Debug            bool                      `json:"debug"`
	Limit            int                       `json:"limit"`
	PoolSize         int                       `json:"pool_size"`
	Delay            int                       `json:"delay"`
	NTP              string                    `json:"ntp"`
	ReconnectTimeout int                       `json:"reconnect_timeout"`
	DownloadDir      string                    `json:"download_dir"`
	Filename         string                    `json:"filename"`
	FilenameMax      int                       `json:"filename_max_length"`
	TriggerReactions []string                  `json:"trigger_reactions"`
	Include          []string                  `json:"include"`
	Exclude          []string                  `json:"exclude"`
	FileSizeMinMB    int64                     `json:"file_size_min_mb"`
	FileSizeMaxMB    int64                     `json:"file_size_max_mb"`
	HTTP             HTTPConfig                `json:"http"`
	WebUI            WebUIConfig               `json:"webui"`
	Modules          ModulesConfig             `json:"modules"`
	Downloader       DownloaderConfig          `json:"downloader"`
	Aria2            Aria2Config               `json:"aria2"`
	Bot              BotConfig                 `json:"bot"`
	Forward          ForwardConfig             `json:"forward"`
}

func (cfg *RuntimeConfig) UnmarshalJSON(data []byte) error {
	type configJSON RuntimeConfig
	decoded := configJSON(*cfg)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*cfg = RuntimeConfig(decoded)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, hasNewMinimum := fields["file_size_min_mb"]; hasNewMinimum {
		return nil
	}
	legacy, hasLegacyMinimum := fields["file_size_mb"]
	if !hasLegacyMinimum {
		return nil
	}
	return json.Unmarshal(legacy, &cfg.FileSizeMinMB)
}
