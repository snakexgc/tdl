package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func TestBotUsesSharedProxyInBothConfigurationModes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Proxy = "socks5://127.0.0.1:1080"
	cfg.ProxyUsername, cfg.ProxyPassword = "shared", "secret"
	cfg.Bot.Proxy = "http://obsolete:secret@127.0.0.1:8000"
	for _, store := range []*rteconfig.Store{nil, rteconfig.NewStore(t.TempDir())} {
		manager := &Manager{componentStore: store}
		require.Equal(t, config.EffectiveProxy(cfg), manager.botProxy(cfg))
		next, err := config.Clone(cfg)
		require.NoError(t, err)
		next.Proxy = ""
		require.Empty(t, manager.botProxy(next), "direct mode cannot revive an obsolete Bot override")
	}
}
