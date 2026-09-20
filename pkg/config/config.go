package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	DownloadExecutorAria2 = "aria2"
	DownloadExecutorLocal = "local"
	DownloadExecutorHTTP  = "http"
)

const (
	ForwardModeDefault = "default"
	ForwardModeClone   = "clone"
)

type (
	BotNotifyConfig  = types.BotNotifyConfig
	BotConfig        = types.BotConfig
	HTTPConfig       = types.HTTPConfig
	WebUIConfig      = types.WebUIConfig
	ModulesConfig    = types.ModulesConfig
	DownloaderConfig = types.DownloaderConfig
	Aria2Config      = types.Aria2Config
	ForwardConfig    = types.ForwardConfig
	Config           = types.RuntimeConfig
)

// DefaultConfig projects the current component schema defaults into an adapter snapshot.
func DefaultConfig() *Config {
	cfg, err := Clone(schemaDefaults())
	if err != nil {
		panic(err)
	}
	return cfg
}

var schemaDefaults = sync.OnceValue(func() *Config {
	cfg, _, err := loadComponents(context.Background(), nil, ports.SystemConfiguration{Namespace: "default"})
	if err != nil {
		panic(err)
	} // Invalid compiled-in schemas are programming errors.
	return cfg
})

func NormalizeNamespace(namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", errors.New("namespace is required")
	}
	for _, r := range namespace {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return "", errors.New("namespace can contain English letters only")
	}
	return namespace, nil
}

func EffectiveProxy(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Proxy)
}

func PrimaryDownloadExecutor(cfg *Config) string {
	if cfg == nil || len(cfg.Downloader.Executors) == 0 {
		return ""
	}
	return cfg.Downloader.Executors[0]
}

func UsesDownloadExecutor(cfg *Config, executor string) bool {
	return cfg != nil && slices.Contains(cfg.Downloader.Executors, executor)
}

func NormalizeForwardMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ForwardModeDefault, nil
	}
	switch mode {
	case ForwardModeDefault, ForwardModeClone:
		return mode, nil
	default:
		return "", fmt.Errorf("forward.mode must be %q or %q", ForwardModeDefault, ForwardModeClone)
	}
}

func HTTPListenAddr(cfg *Config) string {
	return HTTPConfigListenAddr(cfg.HTTP)
}

func HTTPConfigListenAddr(cfg HTTPConfig) string {
	return net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port))
}

func WebUIListenAddr(cfg *Config) string {
	return net.JoinHostPort(cfg.WebUI.Address, strconv.Itoa(cfg.WebUI.Port))
}

func UsesDefaultWebUICredentials(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.WebUI.Username) == schemaDefaults().WebUI.Username && cfg.WebUI.Password == schemaDefaults().WebUI.Password
}

var (
	instance *Config
	mu       sync.RWMutex
	persist  func(context.Context, *Config, *Config) error
)

// Install binds the runtime snapshot to the configuration service.
func Install(cfg *Config, save func(context.Context, *Config, *Config) error) {
	mu.Lock()
	defer mu.Unlock()
	instance = cfg
	persist = save
}

func persistConfig(ctx context.Context, next *Config) error {
	if persist == nil {
		return errors.New("configuration persistence is not installed")
	}
	return persist(ctx, instance, next)
}

// Get 获取配置实例
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return instance
}

// Clone 返回配置的深拷贝，便于在不影响当前实例的情况下修改后再保存。
func Clone(cfg *Config) (*Config, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var next Config
	if err := json.Unmarshal(data, &next); err != nil {
		return nil, err
	}
	return &next, nil
}

// Set 设置配置并保存
func Set(cfg *Config) error {
	mu.Lock()
	defer mu.Unlock()

	if err := persistConfig(context.Background(), cfg); err != nil {
		return err
	}

	instance = cfg
	return nil
}

// CompareAndSet prevents a stale control-plane snapshot from overwriting a
// concurrent configuration update made by another entry point.
func CompareAndSet(ctx context.Context, expected, next *Config) error {
	mu.Lock()
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(instance, expected) {
		return ports.ErrConfigurationConflict
	}
	if err := persistConfig(ctx, next); err != nil {
		return err
	}
	instance = next
	return nil
}
