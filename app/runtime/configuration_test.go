package runtime

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/pkg/config"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

const (
	testLoopbackAddress = "127.0.0.1"
	testAddressField    = "address"
	testPortField       = "port"
)

func TestBotUsesSharedProxy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Proxy = "socks5://shared:secret@127.0.0.1:1080"
	for _, store := range []*rteconfig.Store{nil, newComponentStore(t)} {
		manager := &Manager{componentStore: store}
		require.Equal(t, config.EffectiveProxy(cfg), manager.botProxy(cfg))
		next, err := config.Clone(cfg)
		require.NoError(t, err)
		next.Proxy = ""
		require.Empty(t, manager.botProxy(next), "direct mode cannot revive an obsolete Bot override")
	}
}

func newComponentStore(t *testing.T) *rteconfig.Store {
	t.Helper()
	service, err := configuration.Open(context.Background(), t.TempDir())
	require.NoError(t, err)
	return service.Store()
}

func saveComponent(t *testing.T, store *rteconfig.Store, id string, enabled bool, values map[string]any) {
	t.Helper()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(context.Background(), id, values)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), id, enabled, view))
}

func newStoppedComponentStore(t *testing.T) *rteconfig.Store {
	t.Helper()
	store := newComponentStore(t)
	for _, id := range []string{consoleComponentID, downloadTriggerComponentID, forwardTriggerComponentID, aria2ComponentID, rangeComponentID, panelComponentID, "time.sync"} {
		saveComponent(t, store, id, false, nil)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	saveComponent(t, store, rangeComponentID, true, map[string]any{testAddressField: testLoopbackAddress, testPortField: port})
	return store
}
