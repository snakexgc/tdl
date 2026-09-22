package namingrules

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const testDownloadsRoot = "/downloads"

const shortName = "abc"

func newNaming(t *testing.T, values map[string]any) (*rte.Runtime, ports.NamingRules) {
	t.Helper()
	r := rte.NewRegistry()
	require.NoError(t, Register(r))
	run, err := r.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: values})
	require.NoError(t, err)
	for _, status := range run.Start(context.Background()) {
		require.Equal(t, rte.Running, status.State, status.Detail)
	}
	t.Cleanup(func() { require.NoError(t, run.Stop(context.Background())) })
	value, err := run.Resolve(ports.NamingRulesName)
	require.NoError(t, err)
	return run, value.(ports.NamingRules)
}

func TestRenderAndUniqueKeepConfigurationSnapshot(t *testing.T) {
	run, naming := newNaming(t, map[string]any{filenameField: "F", directoryField: "Y/M", maxBytesField: 32})
	in := ports.NamingInput{BaseDir: `D:\downloads`, Data: ports.NamingData{FileName: strings.Repeat("甲", 40) + ".mp4", DownloadedAt: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)}}
	first, err := naming.Render(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, `D:\downloads\2026\09`, first.Dir)
	require.True(t, utf8.ValidString(first.Out))
	require.LessOrEqual(t, len(first.Out), 32)
	require.NoError(t, run.Reconfigure(context.Background(), ID, map[string]any{filenameField: "F", directoryField: "G", maxBytesField: 8}))
	unique, err := naming.Unique(context.Background(), []ports.NamingResult{first, first})
	require.NoError(t, err)
	require.LessOrEqual(t, len(unique[1].Out), 32)
	require.Greater(t, len(unique[1].Out), 8)
	require.True(t, strings.HasSuffix(unique[1].Out, " (2).mp4"))
	require.True(t, utf8.ValidString(unique[1].Out))
	require.Equal(t, first.Out, unique[0].Out)
}

func TestUniqueRejectsImpossibleSuffixAndHonorsCancellation(t *testing.T) {
	_, naming := newNaming(t, map[string]any{maxBytesField: 3})
	input := []ports.NamingResult{{FileName: shortName, Out: shortName, Dir: testDownloadsRoot, MaxBytes: 3}, {FileName: shortName, Out: shortName, Dir: testDownloadsRoot, MaxBytes: 3}}
	_, err := naming.Unique(context.Background(), input)
	require.ErrorContains(t, err, "cannot fit a conflict suffix")
	require.Equal(t, shortName, input[1].Out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = naming.Render(ctx, ports.NamingInput{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = naming.Unique(ctx, input)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSavedNameIsNotRenderedTwice(t *testing.T) {
	_, naming := newNaming(t, map[string]any{filenameField: "P_S_F", directoryField: "P"})
	got, err := naming.Render(context.Background(), ports.NamingInput{BaseDir: testDownloadsRoot, RenderedName: "album/already.mp4", Data: ports.NamingData{DirectoryID: "123", FileName: "already.mp4"}})
	require.NoError(t, err)
	require.Equal(t, "album/already.mp4", got.FileName)
	require.Equal(t, "/downloads/123/album/already.mp4", got.FullPath)
}

func TestRenderUsesOneSnapshotDuringConcurrentReconfigure(t *testing.T) {
	first := map[string]any{filenameField: "old-F", directoryField: "old", maxBytesField: 255}
	second := map[string]any{filenameField: "new-F", directoryField: "new", maxBytesField: 255}
	run, naming := newNaming(t, first)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			got, err := naming.Render(context.Background(), ports.NamingInput{BaseDir: testDownloadsRoot, Data: ports.NamingData{FileName: "video.mp4"}})
			if err != nil {
				t.Error(err)
				return
			}
			if got.FullPath != "/downloads/old/old-video.mp4" && got.FullPath != "/downloads/new/new-video.mp4" {
				t.Errorf("mixed snapshot: %s", got.FullPath)
				return
			}
		}
	}()
	for range 20 {
		require.NoError(t, run.Reconfigure(context.Background(), ID, second))
		require.NoError(t, run.Reconfigure(context.Background(), ID, first))
	}
	wg.Wait()
}
