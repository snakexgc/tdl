package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	DefaultLimit         = 1
	DefaultPoolSize      = 8
	DefaultHTTPAddress   = "0.0.0.0"
	DefaultHTTPPort      = 22334
	DefaultWebUIAddress  = "0.0.0.0"
	DefaultWebUIPort     = 22335
	DefaultWebUIUsername = "admin"
	DefaultWebUIPassword = "admin"
	DefaultFilename      = "P_S_F"
	DefaultFilenameMax   = 255
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

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Telegram:         types.TelegramCredentialsConfig{BuiltinPreset: "desktop", UseBuiltin: true},
		Namespace:        "default",
		Debug:            false,
		Limit:            DefaultLimit,
		PoolSize:         DefaultPoolSize,
		Delay:            0,
		ReconnectTimeout: 3,
		DownloadDir:      "G\\Y&M",
		Filename:         DefaultFilename,
		FilenameMax:      DefaultFilenameMax,
		TriggerReactions: []string{},
		Include:          []string{},
		Exclude:          []string{},
		FileSizeMinMB:    0,
		FileSizeMaxMB:    0,
		HTTP: HTTPConfig{
			Address:              DefaultHTTPAddress,
			Port:                 DefaultHTTPPort,
			PublicBaseURL:        "",
			DownloadLinkTTLHours: 24,
		},
		WebUI: WebUIConfig{
			Address:  DefaultWebUIAddress,
			Port:     DefaultWebUIPort,
			Username: DefaultWebUIUsername,
			Password: DefaultWebUIPassword,
		},
		Modules: ModulesConfig{
			WebUI:   true,
			Bot:     true,
			Watch:   true,
			HTTP:    true,
			Aria2:   true,
			Forward: false,
		},
		Downloader: DownloaderConfig{
			Executors: []string{DownloadExecutorAria2, DownloadExecutorHTTP},
		},
		Aria2: Aria2Config{
			RPCURL:         "http://127.0.0.1:6800/jsonrpc",
			Secret:         "",
			Dir:            "",
			TimeoutSeconds: 30,
			AutoDownload:   true,
		},
		Bot: BotConfig{
			Token:        "",
			AllowedUsers: []int64{},
			Notify: BotNotifyConfig{
				OnDownloadStart:         false,
				OnDownloadComplete:      false,
				OnDownloadPause:         false,
				OnDownloadError:         false,
				LiveProgress:            false,
				LiveProgressIntervalSec: 5,
			},
		},
		Forward: ForwardConfig{
			Mode:             ForwardModeDefault,
			Target:           "",
			Listen:           []string{},
			ListenComments:   true,
			Silent:           false,
			DedupeTTLSeconds: 600,
			TriggerReactions: []string{},
		},
	}
}

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

func EffectiveLimit(cfg *Config) int {
	if cfg == nil || cfg.Limit < 1 {
		return DefaultLimit
	}
	return cfg.Limit
}

func EffectivePoolSize(cfg *Config) int {
	if cfg == nil || cfg.PoolSize < 1 {
		return DefaultPoolSize
	}
	return cfg.PoolSize
}

func EffectiveProxy(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Proxy)
}

func EffectiveFilename(cfg *Config) string {
	if cfg == nil {
		return DefaultFilename
	}
	filename := strings.TrimSpace(cfg.Filename)
	if filename == "" {
		return DefaultFilename
	}
	return filename
}

func EffectiveFilenameMax(cfg *Config) int {
	if cfg == nil || cfg.FilenameMax <= 0 {
		return DefaultFilenameMax
	}
	return cfg.FilenameMax
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

func validateDownloaders(cfg DownloaderConfig) error {
	if len(cfg.Executors) == 0 {
		return errors.New("downloader.executors cannot be empty")
	}
	seen := map[string]bool{}
	for index, name := range cfg.Executors {
		if !slices.Contains([]string{DownloadExecutorLocal, DownloadExecutorAria2, DownloadExecutorHTTP}, name) || seen[name] {
			return fmt.Errorf("invalid or duplicate download executor %q", name)
		}
		if name == DownloadExecutorHTTP && index != len(cfg.Executors)-1 {
			return errors.New("http must be the final executor")
		}
		seen[name] = true
	}
	if (seen[DownloadExecutorLocal] || cfg.LocalRoot != "") && !filepath.IsAbs(cfg.LocalRoot) {
		return errors.New("local_root must be an absolute local path when local is selected")
	}
	return nil
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

func EffectiveForwardMode(cfg *Config) string {
	if cfg == nil {
		return ForwardModeDefault
	}
	mode, err := NormalizeForwardMode(cfg.Forward.Mode)
	if err != nil {
		return ForwardModeDefault
	}
	return mode
}

func EffectiveForwardDedupeTTL(cfg *Config) int {
	if cfg == nil || cfg.Forward.DedupeTTLSeconds <= 0 {
		return 600
	}
	return cfg.Forward.DedupeTTLSeconds
}

// NormalizeFileSizeRange returns a usable inclusive range in MB. Zero means
// that the corresponding boundary is unlimited. Any invalid range is reset
// as a whole so callers never apply only part of a malformed setting.
func NormalizeFileSizeRange(minMB, maxMB int64) (normalizedMinMB, normalizedMaxMB int64, valid bool) {
	if minMB < 0 || maxMB < 0 || (minMB > 0 && maxMB > 0 && minMB > maxMB) {
		return 0, 0, false
	}
	return minMB, maxMB, true
}

func HTTPListenAddr(cfg *Config) string {
	if cfg == nil {
		return HTTPConfigListenAddr(HTTPConfig{})
	}
	return HTTPConfigListenAddr(cfg.HTTP)
}

func HTTPConfigListenAddr(cfg HTTPConfig) string {
	address := DefaultHTTPAddress
	port := DefaultHTTPPort
	if strings.TrimSpace(cfg.Address) != "" {
		address = strings.TrimSpace(cfg.Address)
	}
	if cfg.Port > 0 {
		port = cfg.Port
	}
	return net.JoinHostPort(address, strconv.Itoa(port))
}

func WebUIListenAddr(cfg *Config) string {
	address := DefaultWebUIAddress
	port := DefaultWebUIPort
	if cfg != nil {
		if strings.TrimSpace(cfg.WebUI.Address) != "" {
			address = strings.TrimSpace(cfg.WebUI.Address)
		}
		if cfg.WebUI.Port > 0 {
			port = cfg.WebUI.Port
		}
	}
	return net.JoinHostPort(address, strconv.Itoa(port))
}

func UsesDefaultWebUICredentials(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.WebUI.Username) == DefaultWebUIUsername && cfg.WebUI.Password == DefaultWebUIPassword
}

func normalizeHTTPConfig(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	httpCfg := &cfg.HTTP
	httpCfg.Address = strings.TrimSpace(httpCfg.Address)

	if httpCfg.Address == "" {
		httpCfg.Address = DefaultHTTPAddress
	}
	if httpCfg.Port == 0 {
		httpCfg.Port = DefaultHTTPPort
	}
	if httpCfg.Port < 1 || httpCfg.Port > 65535 {
		return fmt.Errorf("http.port must be between 1 and 65535")
	}
	return nil
}

func normalizeWebUIConfig(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	web := &cfg.WebUI
	web.Address = strings.TrimSpace(web.Address)

	if web.Address == "" {
		web.Address = DefaultWebUIAddress
	}
	if web.Port == 0 {
		web.Port = DefaultWebUIPort
	}
	if web.Port < 1 || web.Port > 65535 {
		return fmt.Errorf("webui.port must be between 1 and 65535")
	}
	web.Username = strings.TrimSpace(web.Username)
	return nil
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	cfg.Telegram.APIHash = strings.TrimSpace(cfg.Telegram.APIHash)
	cfg.Telegram.BuiltinPreset = strings.TrimSpace(cfg.Telegram.BuiltinPreset)
	if err := application.ValidateTelegramCredentials(cfg.Telegram); err != nil {
		return errors.Wrap(err, "telegram credentials")
	}
	namespace, err := NormalizeNamespace(cfg.Namespace)
	if err != nil {
		return errors.Wrap(err, "validate namespace")
	}
	cfg.Namespace = namespace
	cfg.Proxy = strings.TrimSpace(cfg.Proxy)
	cfg.NTP = strings.TrimSpace(cfg.NTP)
	cfg.Limit = EffectiveLimit(cfg)
	cfg.PoolSize = EffectivePoolSize(cfg)
	cfg.Filename = EffectiveFilename(cfg)
	cfg.FilenameMax = EffectiveFilenameMax(cfg)
	cfg.FileSizeMinMB, cfg.FileSizeMaxMB, _ = NormalizeFileSizeRange(cfg.FileSizeMinMB, cfg.FileSizeMaxMB)
	if err := validateDownloaders(cfg.Downloader); err != nil {
		return err
	}
	forwardMode, err := NormalizeForwardMode(cfg.Forward.Mode)
	if err != nil {
		return err
	}
	cfg.Forward.Mode = forwardMode
	cfg.Forward.Target = strings.TrimSpace(cfg.Forward.Target)
	cfg.Forward.Listen = normalizeStringList(cfg.Forward.Listen)
	if cfg.Forward.DedupeTTLSeconds < 0 {
		return errors.New("forward.dedupe_ttl_seconds must be greater than or equal to 0")
	}
	cfg.Forward.TriggerReactions = normalizeStringList(cfg.Forward.TriggerReactions)
	if cfg.Bot.Notify.LiveProgressIntervalSec < 5 {
		cfg.Bot.Notify.LiveProgressIntervalSec = 5
	}
	if err := normalizeHTTPConfig(cfg); err != nil {
		return err
	}
	if err := normalizeWebUIConfig(cfg); err != nil {
		return err
	}
	return nil
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
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
