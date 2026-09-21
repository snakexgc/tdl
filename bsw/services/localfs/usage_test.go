package localfs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadUsageCountsFilesAndExcludesIncompleteDownloads(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0o755))
	for name, data := range map[string]string{"one.bin": "123", "nested/two.bin": "12345", "partial.bin": "1234", ".tdl-write-test-probe": "x"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0o600))
	}
	incomplete := filepath.Join(root, "partial.bin")
	if runtime.GOOS == "windows" {
		incomplete = strings.ToUpper(incomplete)
	}
	usage := DownloadUsage(context.Background(), root, []string{incomplete})
	require.Empty(t, usage.Errors)
	require.True(t, usage.Exists)
	require.EqualValues(t, 2, usage.FileCount)
	require.EqualValues(t, 8, usage.FileBytes)
	require.Positive(t, usage.TotalBytes)
	require.LessOrEqual(t, usage.FreeBytes, usage.TotalBytes)
}

func TestDownloadUsageDoesNotCreateMissingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created", "downloads")
	usage := DownloadUsage(context.Background(), root, nil)
	require.Empty(t, usage.Errors)
	require.False(t, usage.Exists)
	require.Zero(t, usage.FileCount)
	require.Positive(t, usage.TotalBytes)
	require.NoDirExists(t, root)
}

func TestDownloadUsageReportsInvalidRootAndCanceledScan(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("abc"), 0o600))
	usage := DownloadUsage(context.Background(), file, nil)
	require.Contains(t, usage.Errors["directory"], "not a directory")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	usage = DownloadUsage(ctx, root, nil)
	require.Contains(t, usage.Errors["directory"], context.Canceled.Error())
}

func TestDownloadUsageDoesNotFollowDirectoryOrFileSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("external"), 0o600))
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, os.Symlink(filepath.Join(outside, "file"), filepath.Join(root, "linked-file")))
	usage := DownloadUsage(context.Background(), root, nil)
	require.Empty(t, usage.Errors)
	require.Zero(t, usage.FileCount)
	require.Zero(t, usage.FileBytes)
}
