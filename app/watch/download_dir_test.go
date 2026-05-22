package watch

import (
	"context"
	"path/filepath"
	"testing"
	"text/template"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/tplfunc"
)

func TestRenderDownloadDirTemplate(t *testing.T) {
	data := downloadDirData{
		ID:               "12345",
		Name:             "Group Name",
		MessageTitle:     "Trigger Title",
		MessageID:        "7",
		TriggerMessageID: "6",
		FileName:         "video.mp4",
		AlbumID:          "999",
		Time:             time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
	}

	require.Equal(t, []string{"2026", "04", "Group Name"}, renderDownloadDir(`Y/M/G`, data))
	require.Equal(t, []string{"202604Group Name"}, renderDownloadDir(`Y&M&G`, data))
	require.Equal(t, []string{"202604", "Group Name", "23"}, renderDownloadDir(`Y&M\G\D`, data))
	require.Equal(t, []string{"Trigger Title", "Group Name"}, renderDownloadDir(`I/G`, data))
	require.Equal(t, []string{"12345", "7", "6", "999"}, renderDownloadDir(`P/S/R/A`, data))
	require.Equal(t, []string{"video.mp4"}, renderDownloadDir(`F`, data))
}

func TestFileNameConfigTemplateAliases(t *testing.T) {
	require.Equal(t,
		`{{ .G }}-{{ .I }}-{{ .P }}-{{ .S }}-{{ .R }}-{{ filenamify .FileName }}`,
		fileNameConfigTemplate("G-I-P-S-R-F"),
	)
	require.Equal(t,
		config.DefaultFilename,
		fileNameConfigTemplate(config.DefaultFilename),
	)
}

func TestRenderFileNameTemplateUsesMessageTitleAndPeerName(t *testing.T) {
	tpl := template.Must(template.New("watch").
		Funcs(tplfunc.FuncMap(tplfunc.All...)).
		Parse(fileNameConfigTemplate("G-I-F")))
	w := &Watcher{tpl: tpl}

	got, err := w.renderFileName(
		12345,
		"Group Name",
		time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC),
		&tg.Message{ID: 8, Date: 1770000000, Message: "media caption"},
		&tg.Message{ID: 7, Date: 1770000000, Message: "Trigger Title"},
		&tmedia.Media{Name: "video.mp4", Size: 1024},
	)

	require.NoError(t, err)
	require.Equal(t, "Group Name-Trigger Title-video.mp4", got)
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
