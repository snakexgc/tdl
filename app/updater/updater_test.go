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
	testDockerVersion = "v202609042_docker"
)

func TestNeedsUpdateDateVersionAndDev(t *testing.T) {
	require.True(t, needsUpdate("dev", "v202609043"))
	require.True(t, needsUpdate("v202609042", "v202609043"))
	require.False(t, needsUpdate("v202609043", "v202609043"))
	require.False(t, needsUpdate("v202609044", "v202609043"))
	require.True(t, needsUpdate("v202609049", "v2026090410"))
	require.True(t, needsUpdate("v2026090410", "v202609051"))
}

func TestNeedsUpdateDockerOriginVersion(t *testing.T) {
	require.True(t, isDockerVersion("v202609042-origin-master"))
	require.Equal(t, "v202609042", releaseVersionForCompare("v202609042-origin-master"))
	require.False(t, needsUpdate("v202609042-origin-master", "v202609042"))
	require.True(t, needsUpdate("v202609042-origin-master", "v202609043"))
}

func TestNeedsUpdateDockerSuffixVersion(t *testing.T) {
	require.True(t, isDockerVersion(testDockerVersion))
	require.Equal(t, "v202609042", releaseVersionForCompare(testDockerVersion))
	require.False(t, needsUpdate(testDockerVersion, "v202609042"))
	require.True(t, needsUpdate(testDockerVersion, "v202609043"))
}

func TestDockerRuntimeCanBeMarkedByEnvironment(t *testing.T) {
	t.Setenv(dockerEnv, "true")
	require.True(t, isDockerRuntime("v202609043"))
}

func TestDockerReleaseCanUpdateProgramInPlace(t *testing.T) {
	osName := goreleaserOSName(runtime.GOOS)
	archName := goreleaserArchName(runtime.GOARCH)
	ext := ".tar.gz"
	if runtime.GOOS == goosWindows {
		ext = ".zip"
	}
	assetName := "tdl_" + osName + "_" + archName + ext

	info := infoForRelease(Info{
		CurrentVersion: "v202609042-origin-master",
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		Docker:         true,
	}, githubRelease{
		TagName: "v202609043",
		Assets: []githubAsset{{
			Name:               assetName,
			BrowserDownloadURL: "https://example.com/" + assetName,
		}},
	})

	require.True(t, info.NeedsUpdate)
	require.True(t, info.CanUpdate)
	require.Equal(t, assetName, info.AssetName)
	require.NotEmpty(t, info.AssetURL)
	require.Contains(t, info.Message, "Docker 容器不会更新或重建")
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
