package watch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	testGroupName    = "Group Name"
	testTriggerTitle = "Trigger Title"
	testVideoFile    = "video.mp4"
	testMP4Extension = "mp4"
	testMKVExtension = "mkv"
	testMediaCaption = "media caption"
)

func TestRenderFileNameTemplateUsesMessageTitleAndPeerName(t *testing.T) {
	w := namingWatcher(t, "G-I-F", 255)

	got, err := renderFileNameForTest(w,
		12345,
		testGroupName,
		time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
		&tg.Message{ID: 8, Date: 1770000000, Message: testMediaCaption},
		&tg.Message{ID: 7, Date: 1770000000, Message: testTriggerTitle},
		&tmedia.Media{Name: testVideoFile, Size: 1024},
	)

	require.NoError(t, err)
	require.Equal(t, testGroupName+"-TriggerTitle-video.mp4", got)
}

func TestRenderFileNameFAndIAlwaysConcatenated(t *testing.T) {
	w := namingWatcher(t, "G-I-F", 255)

	// F contains the same text as I — both must appear; no dedup suppression.
	got, err := renderFileNameForTest(w,
		12345,
		testGroupName,
		time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
		&tg.Message{ID: 8, Date: 1770000000, Message: testMediaCaption},
		&tg.Message{ID: 7, Date: 1770000000, Message: "Hello World"},
		&tmedia.Media{Name: "HelloWorld.mp4", Size: 1024},
	)

	require.NoError(t, err)
	require.Equal(t, testGroupName+"-HelloWorld-HelloWorld.mp4", got)
}

func TestRenderFileNameLengthLimitShrinksOnlyMessageTitleAlias(t *testing.T) {
	pattern := "G-I-F"
	// 50-byte limit: "FullGroupName-" (14) + I + "-video-file.mp4" (15) = 29 fixed bytes; I gets up to 21 bytes.
	w := namingWatcher(t, pattern, 50)

	got, err := renderFileNameForTest(w,
		12345,
		"FullGroupName",
		time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
		&tg.Message{ID: 8, Date: 1770000000, Message: testMediaCaption},
		&tg.Message{ID: 7, Date: 1770000000, Message: strings.Repeat("甲", 80) + strings.Repeat("B", 80)},
		&tmedia.Media{Name: "video-file.mp4", Size: 1024},
	)

	require.NoError(t, err)
	require.LessOrEqual(t, len(got), 50) // byte count
	require.True(t, strings.HasPrefix(got, "FullGroupName-"))
	require.True(t, strings.HasSuffix(got, "-video-file.mp4"))
	require.Contains(t, got, "...")
}

func TestRenderFileNameLengthLimitFallsBackWhenNoMessageTitleAlias(t *testing.T) {
	pattern := "F"
	// 20-byte limit; fallback hard-truncates, no "..." inserted.
	w := namingWatcher(t, pattern, 20)

	got, err := renderFileNameForTest(w,
		12345,
		testGroupName,
		time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
		&tg.Message{ID: 8, Date: 1770000000, Message: testMediaCaption},
		&tg.Message{ID: 7, Date: 1770000000, Message: testTriggerTitle},
		&tmedia.Media{Name: strings.Repeat("a", 80) + ".mp4", Size: 1024},
	)

	require.NoError(t, err)
	require.Len(t, got, 20) // byte count (all ASCII here)
	require.True(t, strings.HasSuffix(got, ".mp4"))
	require.NotContains(t, got, "...")
}

func TestJoinTargetPathKeepsTargetFilesystemStyle(t *testing.T) {
	require.Equal(t, `D:\Download\202604\12345\Group`, joinTargetPath(`D:\Download`, "202604", "12345", "Group"))
	require.Equal(t, `/root/download/202604/12345/Group`, joinTargetPath(`/root/download`, "202604", "12345", "Group"))
	require.Equal(t, `/202604`, joinTargetPath(`/`, "202604"))
}

func TestResolveTargetPathUsesTargetStyle(t *testing.T) {
	dir, out, full := resolveTargetPath(`D:\Download\202604`, `sub/video.mp4`)
	require.Equal(t, `D:\Download\202604\sub`, dir)
	require.Equal(t, testVideoFile, out)
	require.Equal(t, `D:\Download\202604\sub\video.mp4`, full)

	dir, out, full = resolveTargetPath(`/root/download/202604`, `sub\video.mp4`)
	require.Equal(t, `/root/download/202604/sub`, dir)
	require.Equal(t, testVideoFile, out)
	require.Equal(t, `/root/download/202604/sub/video.mp4`, full)
}

func TestResolveTargetPathDoesNotTraverseParent(t *testing.T) {
	dir, out, full := resolveTargetPath(`/root/download`, `../outside.mp4`)
	require.Equal(t, `/root/download`, dir)
	require.Equal(t, `outside.mp4`, out)
	require.Equal(t, `/root/download/outside.mp4`, full)
}

func TestUniquifyInternalTargetsAddsConflictSuffix(t *testing.T) {
	tasks := []preparedFileTask{
		{fileName: "album/video.mp4", dir: `/downloads/album`, out: testVideoFile, fullPath: `/downloads/album/video.mp4`},
		{fileName: "album/video.mp4", dir: `/downloads/album`, out: testVideoFile, fullPath: `/downloads/album/video.mp4`},
	}

	w := namingWatcher(t, "F", 255)
	got, err := w.uniquifyInternalTargets(context.Background(), tasks)
	require.NoError(t, err)
	require.Equal(t, testVideoFile, got[0].out)
	require.Equal(t, "video (2).mp4", got[1].out)
	require.Equal(t, `/downloads/album/video (2).mp4`, got[1].fullPath)
	require.Equal(t, "album/video (2).mp4", got[1].fileName)
}

func TestPrepareAria2OutputRootUsesConfiguredRemoteDirWithoutLocalAccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "downloads")
	cfg := config.DefaultConfig()
	cfg.Aria2.Dir = root

	got, ensure, err := prepareAria2OutputRoot(context.Background(), fakeAria2GlobalDirGetter{dir: "/ignored"}, cfg)
	require.NoError(t, err)
	require.False(t, ensure)
	require.Equal(t, cleanTargetRoot(root), got)
	require.NoDirExists(t, root)
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

func namingWatcher(t *testing.T, pattern string, maximum int) *Watcher {
	t.Helper()
	opts := Options{Template: pattern, FilenameMaxLength: maximum}
	host, filter, naming, err := startPolicies(context.Background(), "default", opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	opts.Filter, opts.Naming = filter, naming
	return &Watcher{opts: opts}
}

func renderFileNameForTest(w *Watcher, dialogID int64, peerName string, at time.Time, msg, trigger *tg.Message, media *tmedia.Media) (string, error) {
	target, err := w.renderTarget(context.Background(), "", "", dialogID, peerName, at, msg, trigger, media)
	return target.FileName, err
}
