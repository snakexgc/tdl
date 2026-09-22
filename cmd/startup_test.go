package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	bootstrap "github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/rte"
)

type startupProbeFunc func(context.Context, string, time.Duration) (types.TimeSample, error)

func TestStartupConfigurationErrorsAreWrittenToLog(t *testing.T) {
	const helperEnv = "TDL_TEST_INVALID_STARTUP"
	const httpPortPhase = "http-port"
	phase := os.Getenv(helperEnv)
	if phase == "" {
		for _, phase := range []string{"json", httpPortPhase} {
			t.Run(phase, func(t *testing.T) {
				executable, err := os.Executable()
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, executable, "-test.run=^TestStartupConfigurationErrorsAreWrittenToLog$")
				child.Env = append(os.Environ(), helperEnv+"="+phase, consts.EnvHome+"="+t.TempDir())
				output, err := child.CombinedOutput()
				require.NoError(t, err, string(output))
			})
		}
		return
	}
	content := `{"version":`
	if phase == httpPortPhase {
		content = `{"version":1,"system":{"namespace":"default"},"components":{"proxy.range":{"enabled":true,"values":{"port":70000}}}}`
	}
	require.NoError(t, os.WriteFile(filepath.Join(os.Getenv(consts.EnvHome), "tdl_config.json"), []byte(content), 0o600))
	command := New()
	command.SetArgs(nil)
	require.Error(t, command.ExecuteContext(context.Background()))
	data, err := os.ReadFile(filepath.Join(consts.LogPath, "latest.log"))
	require.NoError(t, err)
	require.Contains(t, string(data), "ERROR")
	require.Contains(t, string(data), "TDL 配置或初始化失败")
	if phase == httpPortPhase {
		require.Contains(t, string(data), "proxy.range")
		require.Contains(t, string(data), "port")
	}
}

func (f startupProbeFunc) Query(ctx context.Context, host string, timeout time.Duration) (types.TimeSample, error) {
	return f(ctx, host, timeout)
}

func TestStartupKeepsWebUIAvailableDuringNetworkFailure(t *testing.T) {
	const helperEnv = "TDL_TEST_OFFLINE_STARTUP"
	const successPhase = "success"
	phase := os.Getenv(helperEnv)
	if phase == "" {
		for _, phase := range []string{successPhase, "failure", "cancel"} {
			t.Run(phase, func(t *testing.T) {
				executable, err := os.Executable()
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, executable, "-test.run=^TestStartupKeepsWebUIAvailableDuringNetworkFailure$")
				child.Env = append(os.Environ(), helperEnv+"="+phase, consts.EnvHome+"="+t.TempDir())
				output, err := child.CombinedOutput()
				require.NoError(t, err, string(output))
			})
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := bootstrap.Open(ctx, os.Getenv(consts.EnvHome))
	require.NoError(t, err)
	catalog, err := application.Catalog()
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, service.Store())
	// No real Telegram session or external service is used by this test.
	for _, id := range []string{"console.bot", "downloader.aria2", "downloader.local", "forwarder", "trigger.download", "trigger.forward"} {
		require.NoError(t, directory.SetEnabled(ctx, id, false))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	require.NoError(t, directory.Patch(ctx, "panel.webui", map[string]any{"address": "127.0.0.1", "port": port, "username": "admin", "password": "startup-test"}))
	rangeListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	rangeAddress := rangeListener.Addr().String()
	rangePort := rangeListener.Addr().(*net.TCPAddr).Port
	require.NoError(t, rangeListener.Close())
	require.NoError(t, directory.Patch(ctx, "proxy.range", map[string]any{"address": "127.0.0.1", "port": rangePort}))
	require.NoError(t, directory.Patch(ctx, "time.sync", map[string]any{"server": "selected.ntp"}))

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	startupTimeProbe = startupProbeFunc(func(ctx context.Context, _ string, _ time.Duration) (types.TimeSample, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			if phase == successPhase {
				return types.TimeSample{Offset: time.Minute}, nil
			}
			return types.TimeSample{}, errors.New("simulated network initialization failure")
		case <-ctx.Done():
			return types.TimeSample{}, ctx.Err()
		}
	})
	command := New()
	command.SetArgs(nil)
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("startup did not cancel and drain")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("network preparation did not start")
	}

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, Timeout: 2 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	request := func(method, path, body string) []byte {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+address+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		require.NoError(t, err, "WebUI must be listening before any network discovery")
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, string(data))
		return data
	}
	request(http.MethodGet, "/login", "")
	request(http.MethodPost, "/api/auth/login", `{"username":"admin","password":"startup-test"}`)
	for _, path := range []string{"/", "/static/js/main.js", "/api/status", "/api/config", "/api/components", "/api/components/health", "/api/logs"} {
		request(http.MethodGet, path, "")
	}
	// Independent backends must start before discovery finishes too. A missing
	// local download link returns promptly without a Telegram connection.
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + rangeAddress + "/missing")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusNotFound
	}, 5*time.Second, 10*time.Millisecond, "HTTP backend waited for NTP discovery")
	// Saving while discovery is pending must stay a pending edit, even after
	// discovery finishes. Discovery must never reinstall all saved settings.
	request(http.MethodPatch, "/api/config", `{"values":{"debug":true}}`)
	request(http.MethodPatch, "/api/components", `{"id":"account.telegram","values":{"file_limit":3}}`)
	request(http.MethodPatch, "/api/components", `{"id":"time.sync","values":{"server":"edited.ntp"}}`)
	if phase == "cancel" {
		return // Cleanup cancels the still-pending discovery and drains the listener.
	}
	close(release)
	message := "NTP 同步失败"
	if phase == successPhase {
		message = "NTP 时间已同步"
	}
	require.Eventually(t, func() bool {
		return strings.Contains(string(request(http.MethodGet, "/api/logs", "")), message)
	}, 5*time.Second, 10*time.Millisecond)
	var status struct {
		Config       struct{ Debug bool } `json:"config"`
		ActiveConfig struct{ Debug bool } `json:"active_config"`
	}
	require.NoError(t, json.Unmarshal(request(http.MethodGet, "/api/config", ""), &status))
	require.True(t, status.Config.Debug)
	require.False(t, status.ActiveConfig.Debug)
	var components struct {
		Components []rte.Configuration `json:"components"`
	}
	// Periodic calibration must not rewrite saved preferences or activate edits.
	require.Eventually(t, func() bool {
		require.NoError(t, json.Unmarshal(request(http.MethodGet, "/api/components", ""), &components))
		for _, component := range components.Components {
			if component.ID == "account.telegram" {
				return len(component.Changes) == 1
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	for _, component := range components.Components {
		if component.ID == "account.telegram" {
			require.True(t, component.PendingRestart)
			require.Len(t, component.Changes, 1)
			require.Equal(t, "file_limit", component.Changes[0].Name)
		}
		if component.ID == "time.sync" {
			require.True(t, component.PendingRestart)
			require.Len(t, component.Changes, 1)
			require.Equal(t, "edited.ntp", component.Values["server"])
		}
	}
	var live struct {
		Clock types.ClockStatus `json:"clock"`
	}
	require.NoError(t, json.Unmarshal(request(http.MethodGet, "/api/status", ""), &live))
	require.Equal(t, phase == successPhase, live.Clock.Synchronized)
	if phase == successPhase {
		require.Equal(t, "selected.ntp", live.Clock.Server)
		require.Equal(t, time.Minute, live.Clock.Offset)
	} else {
		require.NotEmpty(t, live.Clock.LastError)
	}
	request(http.MethodGet, "/api/components/health", "")
}
