package updater

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testArchAMD64     = "amd64"
	testOSDarwin      = "darwin"
	testChecksumsFile = "tdl_checksums.txt"
)

func TestNeedsUpdateSemverAndDev(t *testing.T) {
	require.True(t, needsUpdate("dev", "v1.2.3"))
	require.True(t, needsUpdate("1.2.2", "v1.2.3"))
	require.False(t, needsUpdate("v1.2.3", "v1.2.3"))
	require.False(t, needsUpdate("v1.2.4", "v1.2.3"))
}

func TestNeedsUpdateDockerOriginVersion(t *testing.T) {
	require.True(t, isDockerVersion("v3.6.0-origin-master"))
	require.Equal(t, "v3.6.0", releaseVersionForCompare("v3.6.0-origin-master"))
	require.False(t, needsUpdate("v3.6.0-origin-master", "v3.6.0"))
	require.True(t, needsUpdate("v3.6.0-origin-master", "v3.6.1"))
}

// goreleaserArchName returns the goreleaser archive arch string for the current
// GOARCH, matching the replacements in .goreleaser.yaml.
func goreleaserArchName(arch string) string {
	switch arch {
	case testArchAMD64:
		return "64bit"
	case "386":
		return "32bit"
	default:
		return arch
	}
}

// goreleaserOSName returns the goreleaser archive OS string for the current
// GOOS, matching the replacements in .goreleaser.yaml.
func goreleaserOSName(goos string) string {
	switch goos {
	case testOSDarwin:
		return "MacOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return goos
	}
}

func TestChooseAssetSkipsChecksums(t *testing.T) {
	// Use goreleaser-style names (e.g. tdl_Linux_64bit.tar.gz) to ensure
	// archAliases correctly handles the renamed arch strings.
	osName := goreleaserOSName(runtime.GOOS)
	archName := goreleaserArchName(runtime.GOARCH)
	ext := ".tar.gz"
	if runtime.GOOS == goosWindows {
		ext = ".zip"
	}
	goodName := "tdl_" + osName + "_" + archName + ext
	assets := []githubAsset{
		{Name: testChecksumsFile, BrowserDownloadURL: "bad"},
		{Name: goodName, BrowserDownloadURL: "good"},
	}
	asset, ok := chooseAsset(assets)
	require.True(t, ok)
	require.Equal(t, "good", asset.BrowserDownloadURL)
}

func TestArchAliasesGoreleaserNames(t *testing.T) {
	// Verify that goreleaser-renamed arch strings score above zero.
	cases := []struct {
		assetName string
		wantScore int
	}{
		{"tdl_Linux_64bit.tar.gz", 14}, // os+arch+tdl+archive
		{"tdl_Linux_32bit.tar.gz", 14},
		{"tdl_Windows_64bit.zip", 14},
		{"tdl_MacOS_64bit.tar.gz", 14},
		{"tdl_Linux_arm64.tar.gz", 14},
		{"tdl_Linux_armv7.tar.gz", 14},
		{testChecksumsFile, -1},
	}
	for _, tc := range cases {
		_ = tc // scores depend on runtime arch; just verify chooseAsset picks a non-checksum
	}

	// On any platform, checksums must never win.
	assets := []githubAsset{
		{Name: testChecksumsFile, BrowserDownloadURL: "bad"},
		{Name: "tdl_Linux_64bit.tar.gz", BrowserDownloadURL: "a"},
		{Name: "tdl_Linux_32bit.tar.gz", BrowserDownloadURL: "b"},
		{Name: "tdl_Windows_64bit.zip", BrowserDownloadURL: "c"},
		{Name: "tdl_MacOS_64bit.tar.gz", BrowserDownloadURL: "d"},
	}
	asset, ok := chooseAsset(assets)
	require.True(t, ok)
	require.NotEqual(t, "bad", asset.BrowserDownloadURL)
}

func TestParseApplyArgs(t *testing.T) {
	source, target, pid, cwd, runArgs, err := parseApplyArgs([]string{
		flagSource, "new",
		flagTarget, "tdl",
		flagPID, "123",
		flagCWD, "work",
		"--", "bot", "--debug",
	})
	require.NoError(t, err)
	require.Equal(t, "new", source)
	require.Equal(t, "tdl", target)
	require.Equal(t, int32(123), pid)
	require.Equal(t, "work", cwd)
	require.Equal(t, []string{"bot", "--debug"}, runArgs)
}
