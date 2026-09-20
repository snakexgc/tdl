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
	removed := []string{".tdl/account-a.db", ".tdl/account-b.db", ".tdl/log/latest.log", ".tdl/nested/.hidden", "tdl_config.json", ".tdl_config.json.tmp-test"}
	for _, name := range removed {
		fixtureFile(t, home, name)
	}
	for _, name := range []string{"tdl.exe", "downloads/video.mp4", "notes.txt", "config.json", "components/unrelated.json"} {
		fixtureFile(t, home, name)
	}
	plan := New(home)
	targets, err := plan.Targets()
	require.NoError(t, err)
	require.Len(t, targets, 2)
	require.FileExists(t, filepath.Join(home, unifiedConfigFile), "preflight must not delete data")
	require.NoError(t, plan.Execute())
	for _, name := range removed {
		require.NoFileExists(t, filepath.Join(home, name))
	}
	for _, name := range []string{dataDirectory} {
		entries, err := os.ReadDir(filepath.Join(home, name))
		require.NoError(t, err)
		require.Empty(t, entries, "keep mount roots but empty their contents")
	}
	for _, name := range []string{"tdl.exe", "downloads/video.mp4", "notes.txt", "config.json", "components/unrelated.json"} {
		require.FileExists(t, filepath.Join(home, name))
	}
	require.NoError(t, plan.Execute(), "reset is safe to retry after completion")
}

func TestResetRejectsInvalidRootsAndPreflightsBeforeDeletingAnything(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{"", ".", filepath.VolumeName(home) + string(filepath.Separator)} {
		require.Error(t, New(path).Execute())
	}
	keep := fixtureFile(t, home, ".tdl/account.db")
	require.NoError(t, os.Mkdir(filepath.Join(home, unifiedConfigFile), 0o700))
	require.Error(t, New(home).Execute())
	require.FileExists(t, keep)
}

func TestResetNeverTraversesLinksOutsideItsTargets(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	keep := fixtureFile(t, outside, "keep.txt")
	link := filepath.Join(home, dataDirectory)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.Error(t, New(home).Execute(), "root links must be refused before reset")
	require.FileExists(t, keep)
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Mkdir(link, 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(link, "nested-link")))
	require.NoError(t, New(home).Execute(), "nested links are removed, not traversed")
	require.FileExists(t, keep)
}
