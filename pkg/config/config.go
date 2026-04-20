package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-faster/errors"
)

// Config 全局配置结构
type Config struct {
	Storage          map[string]string `json:"storage"`
	Proxy            string            `json:"proxy"`
	Namespace        string            `json:"namespace"`
	Debug            bool              `json:"debug"`
	Threads          int               `json:"threads"`
	Limit            int               `json:"limit"`
	PoolSize         int               `json:"pool_size"`
	Delay            int               `json:"delay"`
	NTP              string            `json:"ntp"`
	ReconnectTimeout int               `json:"reconnect_timeout"`
	DownloadDir      string            `json:"download_dir"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Storage: map[string]string{
			"type": "bolt",
			"path": ".tdl/data",
		},
		Namespace:        "default",
		Debug:            false,
		Threads:          4,
		Limit:            2,
		PoolSize:         8,
		Delay:            0,
		ReconnectTimeout: 300,
		DownloadDir:      "downloads",
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

	cfg := &Config{}
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
