package reset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixtureFile(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("test fixture"), 0o600))
	return path
}

func TestResetClearsAllAccountsAndConfigurationWithoutRemovingUnrelatedFiles(t *testing.T) {
	home := t.TempDir()
	removed := []string{".tdl/account-a.db", ".tdl/account-b.db", ".tdl/log/latest.log", ".tdl/nested/.hidden", "config.json", ".config.json.tmp-test", "tdl_config.json", ".tdl_config.json.tmp-test", "components/account-a/swc-panel.webui.json", "components/account-b/secrets/swc-account.telegram-old.json"}
	for _, name := range removed {
		fixtureFile(t, home, name)
	}
	for _, name := range []string{"tdl.exe", "downloads/video.mp4", "notes.txt"} {
		fixtureFile(t, home, name)
	}
	plan := New(home, filepath.Join(home, "components", "account-a"))
	targets, err := plan.Targets()
	require.NoError(t, err)
	require.Len(t, targets, 4)
	require.FileExists(t, filepath.Join(home, "config.json"), "preflight must not delete data")
	require.NoError(t, plan.Execute())
	for _, name := range removed {
		require.NoFileExists(t, filepath.Join(home, name))
	}
	for _, name := range []string{dataDirectory, componentDirectory} {
		entries, err := os.ReadDir(filepath.Join(home, name))
		require.NoError(t, err)
		require.Empty(t, entries, "keep mount roots but empty their contents")
	}
	for _, name := range []string{"tdl.exe", "downloads/video.mp4", "notes.txt"} {
		require.FileExists(t, filepath.Join(home, name))
	}
	require.NoError(t, plan.Execute(), "reset is safe to retry after completion")
}

func TestResetCustomConfigurationRemovesOnlyOwnedDocumentsAndSecretGenerations(t *testing.T) {
	home, extra := t.TempDir(), t.TempDir()
	removed := []string{"swc-account.telegram.json", "migration.json", ".config-interrupted", "secrets/swc-account.telegram-old.json", "secrets/swc-account.telegram-new.json"}
	for _, name := range removed {
		fixtureFile(t, extra, name)
	}
	for _, name := range []string{"notes.json", "secrets/other.json", "other/state.db", "swc-not-a-file.json/keep.txt"} {
		fixtureFile(t, extra, name)
	}
	plan := New(home, extra)
	targets, err := plan.Targets()
	require.NoError(t, err)
	require.Len(t, targets, 5)
	require.NoError(t, plan.Execute())
	for _, name := range removed {
		require.NoFileExists(t, filepath.Join(extra, name))
	}
	for _, name := range []string{"notes.json", "secrets/other.json", "other/state.db", "swc-not-a-file.json/keep.txt"} {
		require.FileExists(t, filepath.Join(extra, name))
	}
}

func TestResetRejectsInvalidRootsAndPreflightsBeforeDeletingAnything(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{"", ".", filepath.VolumeName(home) + string(filepath.Separator)} {
		require.Error(t, New(path, "").Execute())
	}
	keep := fixtureFile(t, home, ".tdl/account.db")
	require.NoError(t, os.Mkdir(filepath.Join(home, configFile), 0o700))
	require.Error(t, New(home, "").Execute())
	require.FileExists(t, keep)
}

func TestResetNeverTraversesLinksOutsideItsTargets(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	keep := fixtureFile(t, outside, "keep.txt")
	link := filepath.Join(home, dataDirectory)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.Error(t, New(home, "").Execute(), "root links must be refused before reset")
	require.FileExists(t, keep)
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Mkdir(link, 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(link, "nested-link")))
	require.NoError(t, New(home, "").Execute(), "nested links are removed, not traversed")
	require.FileExists(t, keep)
}
