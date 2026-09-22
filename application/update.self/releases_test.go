package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte/platform"
)

const (
	testReleaseRepository = "test/repo"
	testOlderVersion      = "v202608011"
	testCurrentRelease    = "current"
	testInvalidRelease    = "invalid"
	testNoReleaseAsset    = "no-asset"
)

func releaseTestInfo() Info {
	return Info{CurrentVersion: "v202609221", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Runtime: runtimeBinary}
}

func releaseTestAsset() string {
	return releaseAssetName(runtime.GOOS, runtime.GOARCH, platform.BuildMetadata().GOARM)
}

func testRelease(tag string, preview bool, published int) githubRelease {
	return githubRelease{
		TagName: tag, Name: "Release " + tag, HTMLURL: "https://github.com/test/repo/releases/tag/" + tag,
		Body: "Notes for " + tag, Prerelease: preview,
		PublishedAt: time.Date(2026, 9, published, 0, 0, 0, 0, time.UTC),
		Assets:      []githubAsset{{Name: releaseTestAsset(), BrowserDownloadURL: "https://example.com/asset"}},
	}
}

func TestReleaseCatalogPaginatesAndKeepsFivePerChannel(t *testing.T) {
	first := []githubRelease{testRelease("preview-first", true, 30)}
	for i := 1; i <= 7; i++ {
		first = append(first, testRelease(fmt.Sprintf("stable-%d", i), false, i))
	}
	draft := testRelease("draft", true, 31)
	draft.Draft = true
	first = append(first, draft, githubRelease{})
	second := []githubRelease{first[0]}
	for i := 1; i <= 7; i++ {
		second = append(second, testRelease(fmt.Sprintf("preview-%d", i), true, i))
	}
	pages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/test/repo/releases", r.URL.Path)
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		require.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		switch page {
		case "1":
			w.Header().Set("Link", `<https://not-followed.invalid>; rel="next"`)
			require.NoError(t, json.NewEncoder(w).Encode(first))
		case "2":
			w.Header().Set("Link", `<https://not-followed.invalid>; rel="next"`)
			require.NoError(t, json.NewEncoder(w).Encode(second))
		default:
			t.Error("unnecessary release page requested")
			http.Error(w, "unexpected page", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := releaseClient{server.Client(), server.URL, testReleaseRepository}
	info, err := client.versions(context.Background(), releaseTestInfo())
	require.NoError(t, err)
	require.Equal(t, []string{"1", "2"}, pages)
	require.Len(t, info.StableReleases, 5)
	require.Len(t, info.PreviewReleases, 5)
	require.Equal(t, "stable-7", info.StableReleases[0].Version)
	require.Equal(t, "stable-3", info.StableReleases[4].Version)
	require.Equal(t, "preview-first", info.PreviewReleases[0].Version)
	require.Equal(t, "preview-4", info.PreviewReleases[4].Version)
	for _, release := range info.StableReleases {
		require.False(t, release.Prerelease)
		require.True(t, release.CanInstall)
		require.Equal(t, "Notes for "+release.Version, release.ReleaseNotes)
	}
	for _, release := range info.PreviewReleases {
		require.True(t, release.Prerelease)
		require.True(t, release.CanInstall)
	}
}

func TestReleaseCatalogAllowsMissingChannelsAndReportsFailures(t *testing.T) {
	status, payload := http.StatusOK, `[]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()
	client := releaseClient{server.Client(), server.URL, testReleaseRepository}
	info, err := client.versions(context.Background(), releaseTestInfo())
	require.NoError(t, err)
	require.Empty(t, info.StableReleases)
	require.Empty(t, info.PreviewReleases)
	require.Contains(t, info.Message, "没有")
	payload = `[{"tag_name":"preview","prerelease":true,"body":"preview notes"}]`
	info, err = client.versions(context.Background(), releaseTestInfo())
	require.NoError(t, err)
	require.Empty(t, info.StableReleases)
	require.Len(t, info.PreviewReleases, 1)
	require.Equal(t, "preview notes", info.PreviewReleases[0].ReleaseNotes)
	status = http.StatusForbidden
	_, err = client.versions(context.Background(), releaseTestInfo())
	require.ErrorContains(t, err, "403")
	status, payload = http.StatusOK, `{invalid`
	_, err = client.versions(context.Background(), releaseTestInfo())
	require.ErrorContains(t, err, "decode releases")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.versions(ctx, releaseTestInfo())
	require.ErrorIs(t, err, context.Canceled)
}

func TestSelectedVersionRejectsUnavailableReleases(t *testing.T) {
	for _, kind := range []string{testCurrentRelease, "draft", "wrong-tag", testNoReleaseAsset, "no-url", runtimeDocker, testInvalidRelease, "missing"} {
		t.Run(kind, func(t *testing.T) {
			info := releaseTestInfo()
			tag := testOlderVersion
			release := testRelease(tag, false, 1)
			switch kind {
			case testCurrentRelease:
				info.CurrentVersion = tag
			case "draft":
				release.Draft = true
			case "wrong-tag":
				release.TagName = "different"
			case testNoReleaseAsset:
				release.Assets = nil
			case "no-url":
				release.Assets[0].BrowserDownloadURL = ""
			case runtimeDocker:
				info.Docker = true
			case testInvalidRelease:
				tag = " "
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if kind == "missing" {
					http.NotFound(w, r)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(release))
			}))
			defer server.Close()
			client := releaseClient{server.Client(), server.URL, testReleaseRepository}
			_, _, err := client.downloadVersion(context.Background(), info, tag)
			require.Error(t, err)
			if kind == runtimeDocker || kind == testInvalidRelease {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls, "rejected selection must not download any assets")
			}
			if kind == testCurrentRelease || kind == testNoReleaseAsset || kind == runtimeDocker {
				choice := releaseChoice(info, release)
				require.False(t, choice.CanInstall)
				require.NotEmpty(t, choice.ReleaseNotes, "unavailable versions still have readable notes")
			}
		})
	}
}

func TestSelectedVersionDownloadsExactPreviewOrOlderRelease(t *testing.T) {
	for _, tag := range []string{testOlderVersion, "v20260922_dev_abc1234", "preview/branch"} {
		t.Run(tag, func(t *testing.T) {
			archive := testReleaseArchive(t, []byte("selected binary: "+tag))
			requests := []string{}
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.EscapedPath())
				switch r.URL.EscapedPath() {
				case "/repos/test/repo/releases/tags/" + url.PathEscape(tag):
					release := testRelease(tag, tag != testOlderVersion, 1)
					release.Assets[0].BrowserDownloadURL = server.URL + "/selected-asset"
					require.NoError(t, json.NewEncoder(w).Encode(release))
				case "/selected-asset":
					_, _ = w.Write(archive)
				default:
					t.Error("unexpected latest or asset request:", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := releaseClient{server.Client(), server.URL, testReleaseRepository}
			plan, info, err := client.downloadVersion(context.Background(), releaseTestInfo(), tag)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(filepath.Dir(plan.SourcePath))) })
			require.Equal(t, tag, plan.Version)
			require.Equal(t, tag, info.LatestVersion)
			require.True(t, info.CanUpdate)
			require.True(t, info.NeedsUpdate)
			binary, err := os.ReadFile(plan.SourcePath)
			require.NoError(t, err)
			require.Equal(t, "selected binary: "+tag, string(binary))
			require.Equal(t, []string{"/repos/test/repo/releases/tags/" + url.PathEscape(tag), "/selected-asset"}, requests)
		})
	}
}

func testReleaseArchive(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if runtime.GOOS == goosWindows {
		writer := zip.NewWriter(&buffer)
		file, err := writer.Create(executableFileName())
		require.NoError(t, err)
		_, err = file.Write(data)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
	} else {
		gzipWriter := gzip.NewWriter(&buffer)
		writer := tar.NewWriter(gzipWriter)
		require.NoError(t, writer.WriteHeader(&tar.Header{Name: executableFileName(), Size: int64(len(data)), Mode: 0o755, Typeflag: tar.TypeReg}))
		_, err := writer.Write(data)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		require.NoError(t, gzipWriter.Close())
	}
	return buffer.Bytes()
}
