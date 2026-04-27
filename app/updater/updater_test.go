package updater

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNeedsUpdateSemverAndDev(t *testing.T) {
	require.True(t, needsUpdate("dev", "v1.2.3"))
	require.True(t, needsUpdate("1.2.2", "v1.2.3"))
	require.False(t, needsUpdate("v1.2.3", "v1.2.3"))
	require.False(t, needsUpdate("v1.2.4", "v1.2.3"))
}

func TestChooseAssetSkipsChecksums(t *testing.T) {
	assets := []githubAsset{
		{Name: "tdl_checksums.txt", BrowserDownloadURL: "bad"},
		{Name: "tdl_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip", BrowserDownloadURL: "good"},
	}
	asset, ok := chooseAsset(assets)
	require.True(t, ok)
	require.Equal(t, "good", asset.BrowserDownloadURL)
}

func TestParseApplyArgs(t *testing.T) {
	source, target, pid, cwd, runArgs, err := parseApplyArgs([]string{
		"--source", "new",
		"--target", "tdl",
		"--pid", "123",
		"--cwd", "work",
		"--", "bot", "--debug",
	})
	require.NoError(t, err)
	require.Equal(t, "new", source)
	require.Equal(t, "tdl", target)
	require.Equal(t, int32(123), pid)
	require.Equal(t, "work", cwd)
	require.Equal(t, []string{"bot", "--debug"}, runArgs)
}
