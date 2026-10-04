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

const retiredClockServerField = "server"

func TestRetiredTimePreferencesAreDroppedWithoutStartupWrites(t *testing.T) {
	for _, tc := range []struct {
		name         string
		values       map[string]any
		legacy       bool
		missingClock bool
	}{
		{name: "current preference", values: map[string]any{retiredClockServerField: "explicit.ntp"}},
		{name: "both preferences", values: map[string]any{retiredClockServerField: "explicit.ntp"}, legacy: true},
		{name: "malformed retired preference", values: map[string]any{retiredClockServerField: []string{"ignored"}}, legacy: true},
		{name: "legacy preference without clock component", legacy: true, missingClock: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, home := context.Background(), t.TempDir()
			service, err := configuration.Open(ctx, home)
			require.NoError(t, err)
			var original manager.Document
			require.NoError(t, json.Unmarshal(readFile(t, home), &original))
			if tc.legacy {
				original.Components["account.telegram"].Values["ntp"] = "legacy.ntp"
			}
			original.Components["account.telegram"].Values["file_limit"] = 7
			if tc.missingClock {
				delete(original.Components, "time.sync")
			} else {
				values := map[string]any{"sync_interval_seconds": 60}
				for field, value := range tc.values {
					values[field] = value
				}
				original.Components["time.sync"] = manager.Component{Enabled: false, Values: values}
			}
			data, err := json.Marshal(original)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, manager.Filename), data, 0o600))
			completed, err := service.Complete(ctx, original)
			require.NoError(t, err)
			after, err := json.Marshal(original)
			require.NoError(t, err)
			require.Equal(t, data, after, "completion must not mutate caller data")
			require.NotContains(t, completed.Components["account.telegram"].Values, "ntp")
			require.NotContains(t, completed.Components["time.sync"].Values, retiredClockServerField)
			loaded, err := configuration.Open(ctx, home)
			require.NoError(t, err)
			clock, err := loaded.Store().Load(ctx, "time.sync")
			require.NoError(t, err)
			require.Equal(t, tc.missingClock, clock.Enabled)
			require.NotContains(t, clock.Values, retiredClockServerField)
			catalog, err := application.Catalog()
			require.NoError(t, err)
			view, err := catalog.View(ctx, "time.sync", clock.Values)
			require.NoError(t, err)
			var interval int64
			require.NoError(t, view.Get("sync_interval_seconds", &interval))
			if tc.missingClock {
				require.EqualValues(t, 300, interval)
			} else {
				require.EqualValues(t, 60, interval)
			}
			require.Equal(t, data, readFile(t, home), "loading must not rewrite the configuration")
			require.NoError(t, rte.NewDirectory(catalog, loaded.Store()).Patch(ctx, "time.sync", map[string]any{"timeout_seconds": 5}))
			var saved manager.Document
			require.NoError(t, json.Unmarshal(readFile(t, home), &saved))
			require.NotContains(t, saved.Components["account.telegram"].Values, "ntp")
			require.NotContains(t, saved.Components["time.sync"].Values, retiredClockServerField)
			require.Len(t, saved.Components["time.sync"].Values, 2)
			require.EqualValues(t, 7, saved.Components["account.telegram"].Values["file_limit"])
			require.EqualValues(t, 5, saved.Components["time.sync"].Values["timeout_seconds"])
			_, err = catalog.View(ctx, "time.sync", map[string]any{retiredClockServerField: "rejected.ntp"})
			require.Error(t, err, "new writes must reject the retired preference")
			original.Components["account.telegram"].Values["unrecognized_time_preference"] = "error"
			_, err = service.Complete(ctx, original)
			require.Error(t, err, "other unknown fields must remain errors")
		})
	}
}
