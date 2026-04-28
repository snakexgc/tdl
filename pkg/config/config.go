package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-faster/errors"
)

// BotConfig Bot 配置
type BotConfig struct {
	Token        string  `json:"token"`
	AllowedUsers []int64 `json:"allowed_users"`
}

type HTTPConfig struct {
	Listen               string           `json:"listen"`
	PublicBaseURL        string           `json:"public_base_url"`
	DownloadLinkTTLHours int              `json:"download_link_ttl_hours"`
	Buffer               HTTPBufferConfig `json:"buffer"`
}

type HTTPBufferConfig struct {
	Mode   string `json:"mode"`
	SizeMB int    `json:"size_mb"`
}

type WebUIConfig struct {
	Listen   string `json:"listen"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ModulesConfig struct {
	Bot   bool `json:"bot"`
	Watch bool `json:"watch"`
}

type Aria2Config struct {
	RPCURL         string `json:"rpc_url"`
	Secret         string `json:"secret"`
	Dir            string `json:"dir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// Config 全局配置结构
type Config struct {
	Proxy            string        `json:"proxy"`
	Namespace        string        `json:"namespace"`
	Debug            bool          `json:"debug"`
	Threads          int           `json:"threads"`
	Limit            int           `json:"limit"`
	PoolSize         int           `json:"pool_size"`
	Delay            int           `json:"delay"`
	NTP              string        `json:"ntp"`
	ReconnectTimeout int           `json:"reconnect_timeout"`
	DownloadDir      string        `json:"download_dir"`
	TriggerReactions []string      `json:"trigger_reactions"`
	Include          []string      `json:"include"`
	Exclude          []string      `json:"exclude"`
	HTTP             HTTPConfig    `json:"http"`
	WebUI            WebUIConfig   `json:"webui"`
	Modules          ModulesConfig `json:"modules"`
	Aria2            Aria2Config   `json:"aria2"`
	Bot              BotConfig     `json:"bot"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Namespace:        "default",
		Debug:            false,
		Threads:          4,
		Limit:            2,
		PoolSize:         8,
		Delay:            0,
		ReconnectTimeout: 10,
		DownloadDir:      "downloads",
		TriggerReactions: []string{},
		Include:          []string{},
		Exclude:          []string{},
		HTTP: HTTPConfig{
			Listen:               "0.0.0.0:8080",
			PublicBaseURL:        "",
			DownloadLinkTTLHours: 24,
			Buffer: HTTPBufferConfig{
				Mode:   "memory",
				SizeMB: 64,
			},
		},
		WebUI: WebUIConfig{
			Listen:   "127.0.0.1:22335",
			Username: "admin",
			Password: "",
		},
		Modules: ModulesConfig{
			Bot:   true,
			Watch: true,
		},
		Aria2: Aria2Config{
			RPCURL:         "http://127.0.0.1:6800/jsonrpc",
			Secret:         "",
			Dir:            "",
			TimeoutSeconds: 30,
		},
		Bot: BotConfig{
			Token:        "",
			AllowedUsers: []int64{},
		},
	}
}

var (
	instance   *Config
	once       sync.Once
	configPath string
	mu         sync.RWMutex
)

// Init 初始化配置，从 JSON 文件加载
func Init(execDir string) error {
	var err error
	once.Do(func() {
		configPath = filepath.Join(execDir, "config.json")
		instance, err = Load(configPath)
	})
	return err
}

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，创建默认配置
			cfg := DefaultConfig()
			if err := Save(path, cfg); err != nil {
				return nil, errors.Wrap(err, "save default config")
			}
			return cfg, nil
		}
		return nil, errors.Wrap(err, "read config file")
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, errors.Wrap(err, "unmarshal config")
	}

	return cfg, nil
}

// Save 保存配置到文件
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshal config")
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return errors.Wrap(err, "write config file")
	}

	return nil
}

// Get 获取配置实例
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return instance
}

// Set 设置配置并保存
func Set(cfg *Config) error {
	mu.Lock()
	defer mu.Unlock()

	if err := Save(configPath, cfg); err != nil {
		return err
	}

	instance = cfg
	return nil
}

// GetPath 获取配置文件路径
func GetPath() string {
	return configPath
}
