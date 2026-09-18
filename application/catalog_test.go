package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

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
		{"account.telegram", map[string]any{"proxy": "ftp://invalid.test"}},
		{"panel.webui", map[string]any{"username": " "}},
		{"naming.rules", map[string]any{"filename": "{{"}},
	} {
		_, err := catalog.View(context.Background(), input.id, input.values)
		require.Error(t, err, input.id)
	}
}
