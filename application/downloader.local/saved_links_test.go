package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareRootDefaultsToExecutableDirectory(t *testing.T) {
	// Configuration and launch directories can both differ from the executable.
	home, working := t.TempDir(), t.TempDir()
	t.Setenv("TDL_HOME", home)
	t.Chdir(working)
	executable, err := os.Executable()
	require.NoError(t, err)
	want := filepath.Join(filepath.Dir(executable), "download")
	_, statErr := os.Stat(want)
	if os.IsNotExist(statErr) {
		t.Cleanup(func() { require.NoError(t, os.Remove(want)) })
	}
	for _, blank := range []string{"", "  "} {
		root, err := PrepareRoot(blank)
		require.NoError(t, err)
		require.Equal(t, want, root)
		require.DirExists(t, root)
	}
	require.NoDirExists(t, filepath.Join(home, "download"))
	require.NoDirExists(t, filepath.Join(working, "download"))
}
