package updater

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte/platform"
)

const (
	testChecksumsFile = "tdl_checksums.txt"
)

func TestNeedsUpdateDateVersionAndDev(t *testing.T) {
	require.True(t, needsUpdate("dev", "v202609043"))
	require.True(t, needsUpdate("v202609042", "v202609043"))
	require.False(t, needsUpdate("v202609043", "v202609043"))
	require.False(t, needsUpdate("v202609044", "v202609043"))
	require.True(t, needsUpdate("v202609049", "v2026090410"))
	require.True(t, needsUpdate("v2026090410", "v202609051"))
}

func TestNeedsUpdateRejectsUnsupportedVersions(t *testing.T) {
	require.False(t, needsUpdate("v1.2.3", "v202609043"))
	require.False(t, needsUpdate("v202609043", "v1.2.4"))
	require.False(t, needsUpdate("v202609043-origin-abc", "v202609051"))
	require.False(t, needsUpdate("dev", "unrecognized"))
	require.True(t, needsUpdate("v20260904_dev_abc1234", "v202609051"))
}

func TestContainerRuntimeCanBeMarkedByEnvironment(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("Linux container detection")
	}
	t.Setenv("container", "podman")
	require.True(t, isContainerRuntime())
	require.ErrorContains(t, StartApply(Plan{}, "", nil), containerUpdateMessage)
	require.ErrorContains(t, RunApply(nil), containerUpdateMessage)
}

func TestDockerReleaseRequiresNewImage(t *testing.T) {
	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH, platform.BuildMetadata().GOARM)

	info := infoForRelease(Info{
		CurrentVersion: "v202609042",
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
	require.False(t, info.CanUpdate)
	require.Equal(t, assetName, info.AssetName)
	require.NotEmpty(t, info.AssetURL)
	require.Equal(t, containerUpdateMessage, info.Message)
}

func TestChooseAssetSkipsChecksums(t *testing.T) {
	// Only the current release archive can be selected.
	goodName := releaseAssetName(runtime.GOOS, runtime.GOARCH, platform.BuildMetadata().GOARM)
	assets := []githubAsset{
		{Name: testChecksumsFile, BrowserDownloadURL: "bad"},
		{Name: goodName, BrowserDownloadURL: "good"},
	}
	asset, ok := chooseAsset(assets)
	require.True(t, ok)
	require.Equal(t, "good", asset.BrowserDownloadURL)
}

func TestReleaseAssetNames(t *testing.T) {
	for _, tc := range []struct{ os, arch, arm, want string }{
		{goosLinux, "amd64", "", "tdl_Linux_64bit.tar.gz"},
		{"windows", "386", "", "tdl_Windows_32bit.zip"},
		{"darwin", "arm64", "", "tdl_MacOS_arm64.tar.gz"},
		{goosLinux, archARM, "5", "tdl_Linux_armv5.tar.gz"},
		{goosLinux, archARM, "6", "tdl_Linux_armv6.tar.gz"},
		{goosLinux, archARM, "7", "tdl_Linux_armv7.tar.gz"},
		{goosLinux, archARM, "", ""},
		{goosLinux, archARM, "8", ""},
		{goosLinux, "riscv64", "", "tdl_Linux_riscv64.tar.gz"},
		{goosLinux, "loong64", "", "tdl_Linux_loong64.tar.gz"},
		{"freebsd", "amd64", "", ""},
	} {
		require.Equal(t, tc.want, releaseAssetName(tc.os, tc.arch, tc.arm))
	}
}

func TestChooseAssetRequiresExactCurrentPlatform(t *testing.T) {
	for _, name := range []string{"tdl_checksums.txt", windowsExecutable, "tdl_windows_x64.zip", "tdl_linux_amd64.tar.gz", "tdl_MacOS_aarch64.tar.gz", "tdl_Linux_64bit.tgz", "tdl_Windows_arm64.zip", "tdl_Linux_armv6.tar.gz"} {
		if name == releaseAssetName(runtime.GOOS, runtime.GOARCH, platform.BuildMetadata().GOARM) {
			continue
		}
		_, ok := chooseAsset([]githubAsset{{Name: name}})
		require.False(t, ok, name)
	}
}

func TestParseApplyArgs(t *testing.T) {
	source, target, pid, cwd, runArgs, err := parseApplyArgs([]string{
		flagSource, "new",
		flagTarget, unixExecutable,
		flagPID, "123",
		flagCWD, "work",
		"--", "version",
	})
	require.NoError(t, err)
	require.Equal(t, "new", source)
	require.Equal(t, unixExecutable, target)
	require.Equal(t, int32(123), pid)
	require.Equal(t, "work", cwd)
	require.Equal(t, []string{"version"}, runArgs)
}
