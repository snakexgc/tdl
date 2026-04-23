package watch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/config"
)

func TestRenderDownloadDirTemplate(t *testing.T) {
	data := downloadDirData{
		ID:   "12345",
		Name: "Group Name",
		Time: time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	}

	require.Equal(t, []string{"2026", "04", "Group Name"}, renderDownloadDir(`Y/M/G`, data))
	require.Equal(t, []string{"202604Group Name"}, renderDownloadDir(`Y&M&G`, data))
	require.Equal(t, []string{"202604", "Group Name", "23"}, renderDownloadDir(`Y&M\G\D`, data))
	require.Equal(t, []string{"12345", "Group Name"}, renderDownloadDir(`I/G`, data))
}

func TestJoinTargetPathKeepsTargetFilesystemStyle(t *testing.T) {
	require.Equal(t, `D:\Download\202604\12345\Group`, joinTargetPath(`D:\Download`, "202604", "12345", "Group"))
	require.Equal(t, `/root/download/202604/12345/Group`, joinTargetPath(`/root/download`, "202604", "12345", "Group"))
	require.Equal(t, `/202604`, joinTargetPath(`/`, "202604"))
}

func TestResolveTargetPathUsesTargetStyle(t *testing.T) {
	dir, out, full := resolveTargetPath(`D:\Download\202604`, `sub/video.mp4`)
	require.Equal(t, `D:\Download\202604\sub`, dir)
	require.Equal(t, "video.mp4", out)
	require.Equal(t, `D:\Download\202604\sub\video.mp4`, full)

	dir, out, full = resolveTargetPath(`/root/download/202604`, `sub\video.mp4`)
	require.Equal(t, `/root/download/202604/sub`, dir)
	require.Equal(t, "video.mp4", out)
	require.Equal(t, `/root/download/202604/sub/video.mp4`, full)
}

func TestPrepareAria2OutputRootUsesConfiguredDirAndCreatesIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "downloads")
	cfg := config.DefaultConfig()
	cfg.Aria2.Dir = root

	got, ensure, err := prepareAria2OutputRoot(context.Background(), fakeAria2GlobalDirGetter{dir: "/ignored"}, cfg)
	require.NoError(t, err)
	require.True(t, ensure)
	require.Equal(t, filepath.Clean(root), got)
	require.DirExists(t, root)
}

func TestPrepareAria2OutputRootReadsAria2DefaultDir(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Aria2.Dir = ""

	got, ensure, err := prepareAria2OutputRoot(context.Background(), fakeAria2GlobalDirGetter{dir: "/root/download"}, cfg)
	require.NoError(t, err)
	require.False(t, ensure)
	require.Equal(t, "/root/download", got)
}

type fakeAria2GlobalDirGetter struct {
	dir string
	err error
}

func (f fakeAria2GlobalDirGetter) GetGlobalDir(ctx context.Context) (string, error) {
	return f.dir, f.err
}
