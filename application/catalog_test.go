package application

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const sharedProxyField = "proxy"

func TestCatalogValidatesUnavailableResources(t *testing.T) {
	catalog, err := Catalog()
	require.NoError(t, err)
	for _, definition := range catalog.Definitions() {
		_, err := catalog.View(context.Background(), definition.Manifest.ID, nil)
		require.NoError(t, err, definition.Manifest.ID)
	}
	for _, input := range []struct {
		id     string
		values map[string]any
	}{
		{"downloader.aria2", map[string]any{"connect_retry_ms": 1000, "connect_retry_max_ms": 100}},
		{"forwarder", map[string]any{"retry_base_seconds": 20, "retry_max_seconds": 10}},
		{"download.control", map[string]any{"executors": []string{"local"}, "local_root": "relative"}},
		{"account.telegram", map[string]any{"api_id": 1}},
		{"downloader.aria2", map[string]any{"rpc_url": "file:///private"}},
		{"forwarder", map[string]any{"mode": "unsupported"}},
		{"download.control", map[string]any{"mode": "unsupported"}},
		{"account.telegram", map[string]any{sharedProxyField: "ftp://invalid.test"}},
		{"panel.webui", map[string]any{"username": " "}},
		{"naming.rules", map[string]any{"filename": "{{"}},
	} {
		_, err := catalog.View(context.Background(), input.id, input.values)
		require.Error(t, err, input.id)
	}
}

func TestWebUISettingsCoverageAndNavigation(t *testing.T) {
	const logsPath = "/logs"
	catalog, err := Catalog()
	require.NoError(t, err)
	tabs := map[string]bool{}
	for _, tab := range strings.Fields("network download forward account links bot notifications panel system") {
		tabs[tab] = true
	}
	visible := map[string]string{}
	orders := map[string]int{}
	proxyEditors := []string{}
	for _, definition := range catalog.Definitions() {
		m := definition.Manifest
		require.NotEmpty(t, m.Feature.ID, "%s has no feature group", m.ID)
		require.NotEmpty(t, m.Feature.Title, m.ID)
		for _, field := range m.Config {
			if field.Name == sharedProxyField {
				proxyEditors = append(proxyEditors, m.ID+"."+field.Name)
				require.Equal(t, "network", field.SettingsTab)
			}
			require.True(t, tabs[field.SettingsTab], "%s.%s has no settings destination", m.ID, field.Name)
			require.NotEmpty(t, field.SettingsSection, "%s.%s", m.ID, field.Name)
		}
		for _, page := range m.Pages {
			if !page.NavHidden {
				visible[page.Path] = page.Title
				orders[page.Path] = page.Order
			}
		}
	}
	require.Equal(t, []string{"account.telegram.proxy"}, proxyEditors)
	require.Equal(t, map[string]string{"/dashboard": "总览", "/downloads": "下载管理", "/forwards": "转发管理", "/user": "账号管理", "/config": "设置", "/modules": "模块管理", logsPath: "日志管理", "/update": "检查更新"}, visible)
	require.Greater(t, orders[logsPath], orders["/config"])
	require.Less(t, orders[logsPath], orders["/update"])
}
