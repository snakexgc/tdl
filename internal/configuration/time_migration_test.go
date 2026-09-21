package configuration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/rte"
)

func TestTimeConfigurationMigratesWithoutStartupWrites(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{true: "explicit new preference wins", false: "legacy preference migrates"}[explicit], func(t *testing.T) {
			ctx, home := context.Background(), t.TempDir()
			service, err := configuration.Open(ctx, home)
			require.NoError(t, err)
			var original manager.Document
			require.NoError(t, json.Unmarshal(readFile(t, home), &original))
			original.Components["account.telegram"].Values["ntp"] = "legacy.ntp"
			original.Components["account.telegram"].Values["file_limit"] = 7
			want := "legacy.ntp"
			if explicit {
				original.Components["time.sync"] = manager.Component{Enabled: false, Values: map[string]any{"server": "explicit.ntp", "sync_interval_seconds": 60}}
				want = "explicit.ntp"
			} else {
				delete(original.Components, "time.sync")
			}
			data, err := json.Marshal(original)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, manager.Filename), data, 0o600))
			completed, err := service.Complete(ctx, original)
			require.NoError(t, err)
			require.Equal(t, "legacy.ntp", original.Components["account.telegram"].Values["ntp"], "completion must not mutate caller data")
			require.NotContains(t, completed.Components["account.telegram"].Values, "ntp")
			loaded, err := configuration.Open(ctx, home)
			require.NoError(t, err)
			clock, err := loaded.Store().Load(ctx, "time.sync")
			require.NoError(t, err)
			require.Equal(t, !explicit, clock.Enabled)
			catalog, err := application.Catalog()
			require.NoError(t, err)
			view, err := catalog.View(ctx, "time.sync", clock.Values)
			require.NoError(t, err)
			var server string
			require.NoError(t, view.Get("server", &server))
			require.Equal(t, want, server)
			require.Equal(t, data, readFile(t, home), "loading must not rewrite the configuration")
			require.NoError(t, rte.NewDirectory(catalog, loaded.Store()).Patch(ctx, "time.sync", map[string]any{"timeout_seconds": 5}))
			var saved manager.Document
			require.NoError(t, json.Unmarshal(readFile(t, home), &saved))
			require.NotContains(t, saved.Components["account.telegram"].Values, "ntp")
			require.EqualValues(t, 7, saved.Components["account.telegram"].Values["file_limit"])
			require.Equal(t, want, saved.Components["time.sync"].Values["server"])
		})
	}
}
