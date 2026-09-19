package migration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

func TestDefaultComponentBootstrapIsAccountScopedAndNeverOverwritesEdits(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	cfg := legacy.DefaultConfig()
	cfg.Namespace = "alice"
	cfg.Downloader.Mode = legacy.DownloaderModeLocal
	cfg.Aria2.Dir = t.TempDir()
	dir, err := EnsureComponents(ctx, home, cfg)
	require.NoError(t, err)
	rel, err := filepath.Rel(home, dir)
	require.NoError(t, err)
	require.NotContains(t, rel, "..")
	unsafe := ComponentDirectory(home, "../account/one")
	rel, err = filepath.Rel(home, unsafe)
	require.NoError(t, err)
	require.NotContains(t, rel, "..")
	store := config.NewStore(dir)
	doc, err := store.Load(ctx, "download.control")
	require.NoError(t, err)
	require.Equal(t, cfg.Aria2.Dir, doc.Values["local_root"])
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "download.control", map[string]any{"mode": "aria2"})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "download.control", true, view))
	again, err := EnsureComponents(ctx, home, cfg)
	require.NoError(t, err)
	require.Equal(t, dir, again)
	doc, err = store.Load(ctx, "download.control")
	require.NoError(t, err)
	require.Equal(t, "aria2", doc.Values["mode"])
	cfg.Namespace = "second"
	second, err := EnsureComponents(ctx, home, cfg)
	require.NoError(t, err)
	require.NotEqual(t, dir, second)
	require.NoError(t, os.Remove(filepath.Join(second, "migration.json")))
	_, err = EnsureComponents(ctx, home, cfg)
	require.ErrorContains(t, err, "incomplete")
}

func TestBootstrapKeepsLocalAndRemoteDirectoriesIndependent(t *testing.T) {
	absolute, err := filepath.Abs("legacy-downloads")
	require.NoError(t, err)
	explicit := t.TempDir()
	for _, test := range []struct {
		name, mode, local, want string
	}{
		{name: "relative local directory", mode: legacy.DownloaderModeLocal, want: absolute},
		{name: "remote directory is not imported", mode: "aria2"},
		{name: "explicit local directory wins", mode: legacy.DownloaderModeLocal, local: explicit, want: explicit},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := legacy.DefaultConfig()
			cfg.Downloader.Mode, cfg.Downloader.LocalRoot = test.mode, test.local
			cfg.Aria2.Dir = "legacy-downloads"
			directory, err := EnsureComponents(context.Background(), t.TempDir(), cfg)
			require.NoError(t, err)
			doc, err := config.NewStore(directory).Load(context.Background(), "download.control")
			require.NoError(t, err)
			require.Equal(t, test.want, doc.Values["local_root"])
		})
	}
}
