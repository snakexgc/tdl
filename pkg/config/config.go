package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-faster/errors"
)

const (
	DefaultThreads       = 4
	DefaultLimit         = 2
	DefaultPoolSize      = 8
	DefaultHTTPAddress   = "0.0.0.0"
	DefaultHTTPPort      = 22334
	DefaultWebUIAddress  = "0.0.0.0"
	DefaultWebUIPort     = 22335
	DefaultWebUIUsername = "admin"
	DefaultWebUIPassword = "admin"
)

const (
	DownloaderModeAria2    = "aria2"
	DownloaderModeInternal = "internal"
)

const (
	ForwardModeDefault = "default"
	ForwardModeClone   = "clone"
)

// BotConfig Bot 配置
type BotConfig struct {
	Token        string  `json:"token"`
	AllowedUsers []int64 `json:"allowed_users"`
}

type HTTPConfig struct {
	Listen               string           `json:"listen,omitempty"`
	Address              string           `json:"address"`
	Port                 int              `json:"port"`
	PublicBaseURL        string           `json:"public_base_url"`
	DownloadLinkTTLHours int              `json:"download_link_ttl_hours"`
	Buffer               HTTPBufferConfig `json:"buffer"`
}

type HTTPBufferConfig struct {
	Mode   string `json:"mode"`
	SizeMB int    `json:"size_mb"`
}

type WebUIConfig struct {
	Listen   string `json:"listen,omitempty"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ModulesConfig struct {
	Bot     bool `json:"bot"`
	Watch   bool `json:"watch"`
	Forward bool `json:"forward"`
}

type DownloaderConfig struct {
	Mode string `json:"mode"`
}

type Aria2Config struct {
	RPCURL         string `json:"rpc_url"`
	Secret         string `json:"secret"`
	Dir            string `json:"dir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type ForwardConfig struct {
	Mode             string   `json:"mode"`
	Target           string   `json:"target"`
	Listen           []string `json:"listen"`
	ListenComments   bool     `json:"listen_comments"`
	Silent           bool     `json:"silent"`
	DedupeTTLSeconds int      `json:"dedupe_ttl_seconds"`
}

// Config 全局配置结构
type Config struct {
	Proxy            string           `json:"proxy"`
	ProxyUsername    string           `json:"proxy_username"`
	ProxyPassword    string           `json:"proxy_password"`
	Namespace        string           `json:"namespace"`
	Debug            bool             `json:"debug"`
	Threads          int              `json:"threads"`
	Limit            int              `json:"limit"`
	PoolSize         int              `json:"pool_size"`
	Delay            int              `json:"delay"`
	NTP              string           `json:"ntp"`
	ReconnectTimeout int              `json:"reconnect_timeout"`
	DownloadDir      string           `json:"download_dir"`
	TriggerReactions []string         `json:"trigger_reactions"`
	Include          []string         `json:"include"`
	Exclude          []string         `json:"exclude"`
	FileSizeMB       int64            `json:"file_size_mb"`
	HTTP             HTTPConfig       `json:"http"`
	WebUI            WebUIConfig      `json:"webui"`
	Modules          ModulesConfig    `json:"modules"`
	Downloader       DownloaderConfig `json:"downloader"`
	Aria2            Aria2Config      `json:"aria2"`
	Bot              BotConfig        `json:"bot"`
	Forward          ForwardConfig    `json:"forward"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Namespace:        "default",
		Debug:            false,
		Threads:          DefaultThreads,
		Limit:            DefaultLimit,
		PoolSize:         DefaultPoolSize,
		Delay:            0,
		ReconnectTimeout: 3,
		DownloadDir:      "G\\Y&M",
		TriggerReactions: []string{},
		Include:          []string{},
		Exclude:          []string{},
		FileSizeMB:       0,
		HTTP: HTTPConfig{
			Address:              DefaultHTTPAddress,
			Port:                 DefaultHTTPPort,
			PublicBaseURL:        "",
			DownloadLinkTTLHours: 24,
			Buffer: HTTPBufferConfig{
				Mode:   "memory",
				SizeMB: 64,
			},
		},
		WebUI: WebUIConfig{
			Address:  DefaultWebUIAddress,
			Port:     DefaultWebUIPort,
			Username: DefaultWebUIUsername,
			Password: DefaultWebUIPassword,
		},
		Modules: ModulesConfig{
			Bot:     true,
			Watch:   true,
			Forward: false,
		},
		Downloader: DownloaderConfig{
			Mode: DownloaderModeAria2,
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
		Forward: ForwardConfig{
			Mode:             ForwardModeDefault,
			Target:           "",
			Listen:           []string{},
			ListenComments:   true,
			Silent:           false,
			DedupeTTLSeconds: 600,
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

func EffectiveThreads(cfg *Config) int {
	if cfg == nil || cfg.Threads < 1 {
		return DefaultThreads
	}
	return cfg.Threads
}

func EffectiveLimit(cfg *Config) int {
	if cfg == nil || cfg.Limit < 1 {
		return DefaultLimit
	}
	return cfg.Limit
}

func EffectivePoolSize(cfg *Config) int {
	if cfg == nil || cfg.PoolSize < 0 {
		return DefaultPoolSize
	}
	return cfg.PoolSize
}

func EffectiveProxy(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	proxyURL := strings.TrimSpace(cfg.Proxy)
	username := strings.TrimSpace(cfg.ProxyUsername)
	if proxyURL == "" || username == "" {
		return proxyURL
	}

	u, err := url.Parse(proxyURL)
	if err != nil || u.User != nil {
		return proxyURL
	}
	if cfg.ProxyPassword == "" {
		u.User = url.User(username)
	} else {
		u.User = url.UserPassword(username, cfg.ProxyPassword)
	}
	return u.String()
}

func NormalizeDownloaderMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return DownloaderModeAria2, nil
	}
	switch mode {
	case DownloaderModeAria2, DownloaderModeInternal:
		return mode, nil
	default:
		return "", fmt.Errorf("downloader.mode must be %q or %q", DownloaderModeAria2, DownloaderModeInternal)
	}
}

func EffectiveDownloaderMode(cfg *Config) string {
	if cfg == nil {
		return DownloaderModeAria2
	}
	mode, err := NormalizeDownloaderMode(cfg.Downloader.Mode)
	if err != nil {
		return DownloaderModeAria2
	}
	return mode
}

func NormalizeForwardMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "direct" {
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
	if strings.TrimSpace(httpCfg.Listen) != "" {
		address, port, err := splitLegacyListen("http.listen", httpCfg.Listen, DefaultHTTPAddress)
		if err != nil {
			return err
		}
		httpCfg.Address = address
		httpCfg.Port = port
		httpCfg.Listen = ""
	}
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
	if strings.TrimSpace(web.Listen) != "" {
		address, port, err := splitLegacyListen("webui.listen", web.Listen, DefaultWebUIAddress)
		if err != nil {
			return err
		}
		web.Address = address
		web.Port = port
		web.Listen = ""
	}
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

func splitLegacyListen(field, listen, defaultAddress string) (string, int, error) {
	listen = strings.TrimSpace(listen)
	host, portText, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, fmt.Errorf("%s must be host:port: %w", field, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%s has invalid port %q", field, portText)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultAddress
	}
	return host, port, nil
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	namespace, err := NormalizeNamespace(cfg.Namespace)
	if err != nil {
		return errors.Wrap(err, "validate namespace")
	}
	cfg.Namespace = namespace
	cfg.Proxy = strings.TrimSpace(cfg.Proxy)
	cfg.ProxyUsername = strings.TrimSpace(cfg.ProxyUsername)
	cfg.NTP = strings.TrimSpace(cfg.NTP)
	cfg.Threads = EffectiveThreads(cfg)
	cfg.Limit = EffectiveLimit(cfg)
	cfg.PoolSize = EffectivePoolSize(cfg)
	if cfg.FileSizeMB < 0 {
		return errors.New("file_size_mb must be greater than or equal to 0")
	}
	mode, err := NormalizeDownloaderMode(cfg.Downloader.Mode)
	if err != nil {
		return err
	}
	cfg.Downloader.Mode = mode
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
	if err := Validate(cfg); err != nil {
		return nil, errors.Wrap(err, "validate config")
	}

	return cfg, nil
}

// Save 保存配置到文件
func Save(path string, cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return errors.Wrap(err, "validate config")
	}

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
