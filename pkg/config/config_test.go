package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadMergesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"namespace":"custom"}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Equal(t, "custom", cfg.Namespace)
	require.Empty(t, cfg.ProxyUsername)
	require.Empty(t, cfg.ProxyPassword)
	require.Empty(t, cfg.HTTP.Listen)
	require.Equal(t, "0.0.0.0", cfg.HTTP.Address)
	require.Equal(t, 22334, cfg.HTTP.Port)
	require.Equal(t, "0.0.0.0:22334", HTTPListenAddr(cfg))
	require.Equal(t, "memory", cfg.HTTP.Buffer.Mode)
	require.Equal(t, 64, cfg.HTTP.Buffer.SizeMB)
	require.Equal(t, 24, cfg.HTTP.DownloadLinkTTLHours)
	require.Empty(t, cfg.WebUI.Listen)
	require.Equal(t, "0.0.0.0", cfg.WebUI.Address)
	require.Equal(t, 22335, cfg.WebUI.Port)
	require.Equal(t, "0.0.0.0:22335", WebUIListenAddr(cfg))
	require.Equal(t, "admin", cfg.WebUI.Username)
	require.Equal(t, "admin", cfg.WebUI.Password)
	require.True(t, UsesDefaultWebUICredentials(cfg))
	require.True(t, cfg.Modules.Bot)
	require.True(t, cfg.Modules.Watch)
	require.True(t, cfg.Modules.HTTP)
	require.Equal(t, DownloaderModeAria2, cfg.Downloader.Mode)
	require.Equal(t, "http://127.0.0.1:6800/jsonrpc", cfg.Aria2.RPCURL)
	require.Equal(t, 30, cfg.Aria2.TimeoutSeconds)
	require.Equal(t, DefaultThreads, cfg.Threads)
	require.Equal(t, DefaultLimit, cfg.Limit)
	require.Equal(t, DefaultPoolSize, cfg.PoolSize)
	require.Equal(t, "G\\Y&M", cfg.DownloadDir)
	require.Empty(t, cfg.TriggerReactions)
	require.Zero(t, cfg.FileSizeMB)
}

func TestLoadMigratesLegacyHTTPListen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"http":{"listen":"127.0.0.1:23434","public_base_url":"http://127.0.0.1:23434"}}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Empty(t, cfg.HTTP.Listen)
	require.Equal(t, "127.0.0.1", cfg.HTTP.Address)
	require.Equal(t, 23434, cfg.HTTP.Port)
	require.Equal(t, "127.0.0.1:23434", HTTPListenAddr(cfg))
}

func TestLoadMigratesLegacyWebUIListen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"webui":{"listen":"127.0.0.1:23456","username":"alice","password":"secret"}}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Empty(t, cfg.WebUI.Listen)
	require.Equal(t, "127.0.0.1", cfg.WebUI.Address)
	require.Equal(t, 23456, cfg.WebUI.Port)
	require.Equal(t, "127.0.0.1:23456", WebUIListenAddr(cfg))
	require.False(t, UsesDefaultWebUICredentials(cfg))
}

func TestLoadNormalizesDownloaderMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"downloader":{"mode":" INTERNAL "}}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Equal(t, DownloaderModeInternal, cfg.Downloader.Mode)
}

func TestLoadRejectsInvalidDownloaderMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"downloader":{"mode":"unknown"}}`), 0o644))

	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "downloader.mode")
}

func TestLoadKeepsTransferConcurrencyConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"threads":7,"limit":3,"pool_size":0}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 7, cfg.Threads)
	require.Equal(t, 3, cfg.Limit)
	require.Zero(t, cfg.PoolSize)
}

func TestLoadHTTPBufferConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"http":{"buffer":{"mode":"off","size_mb":32}}}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Equal(t, "off", cfg.HTTP.Buffer.Mode)
	require.Equal(t, 32, cfg.HTTP.Buffer.SizeMB)
}

func TestLoadRejectsInvalidNamespace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"namespace":"user1"}`), 0o644))

	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "English letters only")
}

func TestLoadRejectsNegativeFileSizeMB(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"file_size_mb":-1}`), 0o644))

	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "file_size_mb")
}

func TestNormalizeNamespaceAllowsEnglishLetters(t *testing.T) {
	t.Parallel()

	ns, err := NormalizeNamespace(" Alice ")
	require.NoError(t, err)
	require.Equal(t, "Alice", ns)
}

func TestLoadTrimsNTP(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"ntp":" time1.google.com "}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "time1.google.com", cfg.NTP)
}

func TestEffectiveProxyAddsConfiguredCredentials(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Proxy = "socks5://127.0.0.1:1080"
	cfg.ProxyUsername = " alice "
	cfg.ProxyPassword = "p@ss:word"

	require.Equal(t, "socks5://alice:p%40ss%3Aword@127.0.0.1:1080", EffectiveProxy(cfg))
}

func TestEffectiveProxyKeepsInlineCredentials(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Proxy = "http://inline:secret@127.0.0.1:8080"
	cfg.ProxyUsername = "alice"
	cfg.ProxyPassword = "override"

	require.Equal(t, "http://inline:secret@127.0.0.1:8080", EffectiveProxy(cfg))
}

func TestEffectiveProxyRequiresUsernameForSeparateCredentials(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Proxy = "http://127.0.0.1:8080"
	cfg.ProxyPassword = "secret"

	require.Equal(t, "http://127.0.0.1:8080", EffectiveProxy(cfg))
}

func TestSelectStartupNTPPrefersWorkingConfiguredServer(t *testing.T) {
	t.Parallel()

	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		"custom.ntp": {{elapsed: 20 * time.Millisecond}},
		"fast.ntp":   {{elapsed: time.Millisecond}},
	})

	selection := selectStartupNTP(context.Background(), " custom.ntp ", []string{"fast.ntp"}, probe.probe)

	require.Equal(t, "custom.ntp", selection.Host)
	require.Equal(t, "configured", selection.Source)
	require.False(t, selection.ConfiguredFailed)
	require.Equal(t, 1, probe.attempts("custom.ntp"))
	require.Zero(t, probe.attempts("fast.ntp"))
}

func TestSelectStartupNTPFallsBackToFastestBuiltin(t *testing.T) {
	t.Parallel()

	timeoutErr := errors.New("timeout")
	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		"custom.ntp": {
			{err: timeoutErr},
			{err: timeoutErr},
			{err: timeoutErr},
		},
		"slow.ntp": {{elapsed: 50 * time.Millisecond}},
		"fast.ntp": {{elapsed: 5 * time.Millisecond}},
		"bad.ntp":  {{err: timeoutErr}},
	})

	selection := selectStartupNTP(context.Background(), "custom.ntp", []string{"slow.ntp", "fast.ntp", "bad.ntp"}, probe.probe)

	require.Equal(t, "fast.ntp", selection.Host)
	require.Equal(t, "builtin", selection.Source)
	require.True(t, selection.ConfiguredFailed)
	require.Equal(t, 3, probe.attempts("custom.ntp"))
	require.Equal(t, 1, probe.attempts("slow.ntp"))
	require.Equal(t, 1, probe.attempts("fast.ntp"))
	require.Equal(t, 1, probe.attempts("bad.ntp"))
}

func TestSelectStartupNTPUsesSystemTimeWhenNoServerWorks(t *testing.T) {
	t.Parallel()

	timeoutErr := errors.New("timeout")
	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		"custom.ntp": {
			{err: timeoutErr},
			{err: timeoutErr},
			{err: timeoutErr},
		},
		"bad1.ntp": {{err: timeoutErr}},
		"bad2.ntp": {{err: timeoutErr}},
	})

	selection := selectStartupNTP(context.Background(), "custom.ntp", []string{"bad1.ntp", "bad2.ntp"}, probe.probe)

	require.Empty(t, selection.Host)
	require.Equal(t, "system", selection.Source)
	require.True(t, selection.ConfiguredFailed)
	require.Equal(t, 3, probe.attempts("custom.ntp"))
}

func TestSelectFastestBuiltinNTPProbesConcurrently(t *testing.T) {
	t.Parallel()

	servers := []string{"slow.ntp", "fast.ntp", "middle.ntp"}
	allStarted := make(chan struct{})
	var closeAllStarted sync.Once
	var mu sync.Mutex
	started := map[string]struct{}{}

	probe := func(ctx context.Context, host string, _ time.Duration) (time.Duration, error) {
		mu.Lock()
		started[host] = struct{}{}
		if len(started) == len(servers) {
			closeAllStarted.Do(func() {
				close(allStarted)
			})
		}
		mu.Unlock()

		select {
		case <-allStarted:
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return 0, errors.New("ntp probes were not concurrent")
		}

		switch host {
		case "fast.ntp":
			return time.Millisecond, nil
		case "middle.ntp":
			return 10 * time.Millisecond, nil
		default:
			return 50 * time.Millisecond, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	selection := selectFastestBuiltinNTP(ctx, servers, probe)

	require.Equal(t, "fast.ntp", selection.Host)
	require.Equal(t, "builtin", selection.Source)
}

func TestSelectAndSaveStartupNTPSavesSelectedHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	probe := newFakeNTPProbe(map[string][]fakeNTPProbeResult{
		"slow.ntp": {{elapsed: 50 * time.Millisecond}},
		"fast.ntp": {{elapsed: 5 * time.Millisecond}},
	})

	oldProbe := probeNTP
	oldBuiltin := BuiltinNTPServers
	probeNTP = probe.probe
	BuiltinNTPServers = []string{"slow.ntp", "fast.ntp"}

	mu.Lock()
	oldInstance := instance
	oldConfigPath := configPath
	instance = DefaultConfig()
	configPath = path
	mu.Unlock()

	t.Cleanup(func() {
		probeNTP = oldProbe
		BuiltinNTPServers = oldBuiltin
		mu.Lock()
		instance = oldInstance
		configPath = oldConfigPath
		mu.Unlock()
	})

	selection, err := SelectAndSaveStartupNTP(context.Background())
	require.NoError(t, err)
	require.True(t, selection.Saved)
	require.Equal(t, "fast.ntp", selection.Host)

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "fast.ntp", cfg.NTP)
}

type fakeNTPProbeResult struct {
	elapsed time.Duration
	err     error
}

type fakeNTPProbe struct {
	mu      sync.Mutex
	results map[string][]fakeNTPProbeResult
	calls   map[string]int
}

func newFakeNTPProbe(results map[string][]fakeNTPProbeResult) *fakeNTPProbe {
	return &fakeNTPProbe{
		results: results,
		calls:   map[string]int{},
	}
}

func (f *fakeNTPProbe) probe(_ context.Context, host string, _ time.Duration) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	index := f.calls[host]
	f.calls[host]++
	values := f.results[host]
	if index >= len(values) {
		return 0, errors.New("unexpected ntp probe")
	}
	result := values[index]
	return result.elapsed, result.err
}

func (f *fakeNTPProbe) attempts(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[host]
}
