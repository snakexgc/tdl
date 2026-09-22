package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/types"
)

const releasesPerChannel = 5

// CheckVersions lists published releases in both channels. The legacy latest
// release check remains available to the bot's /update_tdl command.
func CheckVersions(ctx context.Context, proxyURL string) (Info, error) {
	info := currentInfo(DefaultRepository)
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return info, err
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	info, err = (releaseClient{client, githubAPIBase, DefaultRepository}).versions(ctx, info)
	if err != nil {
		level := slog.LevelWarn
		if ctx.Err() != nil {
			level = slog.LevelDebug
		}
		slog.Log(ctx, level, "获取软件版本列表失败", "component", ID, "error", err)
	} else {
		slog.Info("软件版本列表检查已完成", "component", ID, "current_version", info.CurrentVersion, "stable_releases", len(info.StableReleases), "preview_releases", len(info.PreviewReleases))
	}
	return info, err
}

// DownloadVersion resolves the selected tag again on the server. It accepts
// older releases and previews, but never trusts an asset URL from the browser.
func DownloadVersion(ctx context.Context, proxyURL, version string) (Plan, Info, error) {
	info := currentInfo(DefaultRepository)
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return Plan{}, info, err
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	return (releaseClient{client, githubAPIBase, DefaultRepository}).downloadVersion(ctx, info, version)
}

func (c releaseClient) downloadVersion(ctx context.Context, info Info, version string) (Plan, Info, error) {
	info, err := c.version(ctx, info, version)
	if err != nil {
		return Plan{}, info, err
	}
	return downloadRelease(ctx, c.http, info)
}

type releaseClient struct {
	http       *http.Client
	baseURL    string
	repository string
}

func (c releaseClient) get(ctx context.Context, suffix string, target any) (http.Header, error) {
	repository := strings.Trim(c.repository, "/")
	if repository == "" {
		repository = DefaultRepository
	}
	u := fmt.Sprintf("%s/repos/%s/releases%s", c.baseURL, repository, suffix)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tdl-updater")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "request releases")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github release status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return nil, errors.Wrap(err, "decode releases")
	}
	return resp.Header, nil
}

func (c releaseClient) versions(ctx context.Context, info Info) (Info, error) {
	stable, preview := make([]githubRelease, 0), make([]githubRelease, 0)
	seen := make(map[string]bool)
	for page := 1; ; page++ {
		var releases []githubRelease
		headers, err := c.get(ctx, fmt.Sprintf("?per_page=100&page=%d", page), &releases)
		if err != nil {
			return info, err
		}
		for _, release := range releases {
			if release.Draft || release.TagName == "" || seen[release.TagName] {
				continue
			}
			seen[release.TagName] = true
			if release.Prerelease {
				preview = append(preview, release)
			} else {
				stable = append(stable, release)
			}
		}
		if len(stable) >= releasesPerChannel && len(preview) >= releasesPerChannel {
			break
		}
		// Only follow the presence of a next page, never an external URL in Link.
		if len(releases) == 0 || !strings.Contains(headers.Get("Link"), `rel="next"`) {
			break
		}
	}
	info.StableReleases = releaseChoices(info, stable)
	info.PreviewReleases = releaseChoices(info, preview)
	info.Message = "选择版本查看 Release Notes，确认后可下载并切换。"
	if len(stable)+len(preview) == 0 {
		info.Message = "暂时没有已发布的版本。"
	}
	if info.Docker {
		info.Message = containerUpdateMessage
	}
	return info, nil
}

func releaseChoices(info Info, releases []githubRelease) []types.UpdateRelease {
	slices.SortStableFunc(releases, func(a, b githubRelease) int { return b.PublishedAt.Compare(a.PublishedAt) })
	choices := make([]types.UpdateRelease, 0, min(len(releases), releasesPerChannel))
	for _, release := range releases[:min(len(releases), releasesPerChannel)] {
		choices = append(choices, releaseChoice(info, release))
	}
	return choices
}

func releaseChoice(info Info, release githubRelease) types.UpdateRelease {
	choice := types.UpdateRelease{
		Version: release.TagName, Name: release.Name, URL: release.HTMLURL,
		ReleaseNotes: release.Body, PublishedAt: release.PublishedAt, Prerelease: release.Prerelease,
		ReleaseNotesHTML: renderReleaseNotes(release.Body),
		Current:          strings.TrimSpace(info.CurrentVersion) == release.TagName,
	}
	asset, found := chooseAsset(release.Assets)
	choice.AssetName = asset.Name
	switch {
	case info.Docker:
		choice.Message = containerUpdateMessage
	case choice.Current:
		choice.Message = "当前正在运行此版本"
	case release.Draft:
		choice.Message = "此版本尚未发布"
	case !found || asset.BrowserDownloadURL == "":
		choice.Message = fmt.Sprintf("未找到适用于 %s/%s 的发布资产", info.GOOS, info.GOARCH)
	default:
		choice.CanInstall = true
		choice.Message = "可以切换到此版本"
	}
	return choice
}

func (c releaseClient) version(ctx context.Context, info Info, version string) (Info, error) {
	if strings.TrimSpace(version) == "" || strings.TrimSpace(version) != version || len(version) > 256 {
		return info, errors.New("请选择有效的目标版本")
	}
	if info.Docker {
		return info, errors.New(containerUpdateMessage)
	}
	var release githubRelease
	if _, err := c.get(ctx, "/tags/"+url.PathEscape(version), &release); err != nil {
		return info, err
	}
	if release.TagName != version || release.Draft {
		return info, errors.New("所选版本不存在或尚未发布，请重新检查更新")
	}
	choice := releaseChoice(info, release)
	if !choice.CanInstall {
		return info, errors.New(choice.Message)
	}
	info = infoForRelease(info, release)
	// An explicit switch may be a downgrade or a preview with a non-date tag.
	info.NeedsUpdate, info.CanUpdate = true, true
	info.Message = "已选择版本 " + version
	return info, nil
}
