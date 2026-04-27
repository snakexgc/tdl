package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/mod/semver"

	"github.com/iyear/tdl/core/util/netutil"
	"github.com/iyear/tdl/pkg/consts"
)

const (
	DefaultRepository = "snakexgc/tdl"
	githubAPIBase     = "https://api.github.com"
	updateTimeout     = 3 * time.Minute
	goosWindows       = "windows"
)

type Info struct {
	CurrentVersion string    `json:"current_version"`
	CurrentCommit  string    `json:"current_commit"`
	CurrentDate    string    `json:"current_date"`
	GOOS           string    `json:"goos"`
	GOARCH         string    `json:"goarch"`
	Repository     string    `json:"repository"`
	LatestVersion  string    `json:"latest_version"`
	LatestName     string    `json:"latest_name"`
	LatestURL      string    `json:"latest_url"`
	ReleaseNotes   string    `json:"release_notes"`
	PublishedAt    time.Time `json:"published_at,omitempty"`
	AssetName      string    `json:"asset_name,omitempty"`
	AssetURL       string    `json:"asset_url,omitempty"`
	NeedsUpdate    bool      `json:"needs_update"`
	CanUpdate      bool      `json:"can_update"`
	Message        string    `json:"message"`
}

type Plan struct {
	SourcePath string `json:"source_path"`
	Version    string `json:"version"`
	AssetName  string `json:"asset_name"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	HTMLURL     string        `json:"html_url"`
	Body        string        `json:"body"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func CheckLatest(ctx context.Context, proxyURL string) (Info, error) {
	return CheckLatestFrom(ctx, DefaultRepository, proxyURL)
}

func CheckLatestFrom(ctx context.Context, repository, proxyURL string) (Info, error) {
	info := currentInfo(repository)
	release, err := fetchLatestRelease(ctx, repository, proxyURL)
	if err != nil {
		return info, err
	}

	info.LatestVersion = release.TagName
	info.LatestName = release.Name
	info.LatestURL = release.HTMLURL
	info.ReleaseNotes = release.Body
	info.PublishedAt = release.PublishedAt
	info.NeedsUpdate = needsUpdate(consts.Version, release.TagName)

	if asset, ok := chooseAsset(release.Assets); ok {
		info.AssetName = asset.Name
		info.AssetURL = asset.BrowserDownloadURL
		info.CanUpdate = info.NeedsUpdate
	} else if info.NeedsUpdate {
		info.Message = fmt.Sprintf("未找到适用于 %s/%s 的发布资产", runtime.GOOS, runtime.GOARCH)
	}

	if !info.NeedsUpdate {
		info.Message = "当前已是最新版本"
	} else if info.CanUpdate {
		info.Message = "发现新版本，可以更新"
	}
	return info, nil
}

func DownloadLatest(ctx context.Context, proxyURL string) (Plan, Info, error) {
	info, err := CheckLatest(ctx, proxyURL)
	if err != nil {
		return Plan{}, info, err
	}
	if !info.NeedsUpdate {
		return Plan{}, info, errors.New("current version is already up to date")
	}
	if !info.CanUpdate || info.AssetURL == "" {
		return Plan{}, info, errors.New(info.Message)
	}

	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return Plan{}, info, err
	}

	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "tdl-update-*")
	if err != nil {
		return Plan{}, info, errors.Wrap(err, "create update directory")
	}
	assetPath := filepath.Join(tmpDir, safeFileName(info.AssetName))
	if err := downloadFile(ctx, client, info.AssetURL, assetPath); err != nil {
		return Plan{}, info, err
	}
	sourcePath, err := extractExecutable(assetPath, info.AssetName, tmpDir)
	if err != nil {
		return Plan{}, info, err
	}
	if err := os.Chmod(sourcePath, 0o755); err != nil {
		return Plan{}, info, errors.Wrap(err, "mark update executable")
	}

	return Plan{
		SourcePath: sourcePath,
		Version:    info.LatestVersion,
		AssetName:  info.AssetName,
	}, info, nil
}

func StartApply(plan Plan, targetPath string, args []string) error {
	if plan.SourcePath == "" {
		return errors.New("update source path is empty")
	}
	if targetPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return errors.Wrap(err, "get executable path")
		}
		targetPath = exe
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errors.Wrap(err, "get working directory")
	}
	helper, err := copyCurrentExecutableToTemp()
	if err != nil {
		return err
	}

	helperArgs := []string{
		"__apply-update",
		"--source", plan.SourcePath,
		"--target", targetPath,
		"--pid", fmt.Sprintf("%d", os.Getpid()),
		"--cwd", cwd,
		"--",
	}
	helperArgs = append(helperArgs, args...)
	return StartAttached(helper, helperArgs, cwd)
}

func RunApply(args []string) error {
	source, target, pid, cwd, runArgs, err := parseApplyArgs(args)
	if err != nil {
		return err
	}
	if pid > 0 {
		if err := waitForPIDExit(context.Background(), pid, 2*time.Minute); err != nil {
			return err
		}
	}
	if err := replaceExecutable(source, target); err != nil {
		return err
	}
	if cwd == "" {
		cwd = filepath.Dir(target)
	}
	if err := StartAttached(target, runArgs, cwd); err != nil {
		return err
	}
	return nil
}

func StartAttached(path string, args []string, cwd string) error {
	procArgs := append([]string{path}, args...)
	proc, err := os.StartProcess(path, procArgs, &os.ProcAttr{
		Dir:   cwd,
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return errors.Wrap(err, "start process")
	}
	return proc.Release()
}

func currentInfo(repository string) Info {
	return Info{
		CurrentVersion: consts.Version,
		CurrentCommit:  consts.Commit,
		CurrentDate:    consts.CommitDate,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		Repository:     repository,
	}
}

func fetchLatestRelease(ctx context.Context, repository, proxyURL string) (githubRelease, error) {
	if repository == "" {
		repository = DefaultRepository
	}
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return githubRelease{}, err
	}

	u := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, strings.Trim(repository, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tdl-updater")

	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, errors.Wrap(err, "request latest release")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return githubRelease{}, fmt.Errorf("github latest release status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, errors.Wrap(err, "decode latest release")
	}
	if release.TagName == "" {
		return githubRelease{}, errors.New("latest release has empty tag")
	}
	return release, nil
}

func newHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, errors.Wrap(err, "parse proxy url")
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			transport.Proxy = http.ProxyURL(u)
		default:
			dialer, err := netutil.NewProxy(proxyURL)
			if err != nil {
				return nil, err
			}
			transport.DialContext = dialer.DialContext
		}
	}
	return &http.Client{Transport: transport, Timeout: updateTimeout}, nil
}

func downloadFile(ctx context.Context, client *http.Client, rawURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "tdl-updater")
	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "download update asset")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download update asset status %d", resp.StatusCode)
	}

	file, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return errors.Wrap(err, "create update asset")
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return errors.Wrap(err, "write update asset")
	}
	return nil
}

func chooseAsset(assets []githubAsset) (githubAsset, bool) {
	bestScore := -1
	var best githubAsset
	for _, asset := range assets {
		score := assetScore(asset.Name)
		if score > bestScore {
			bestScore = score
			best = asset
		}
	}
	return best, bestScore > 0
}

func assetScore(name string) int {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "sha256") || strings.Contains(lower, "checksum") || strings.HasSuffix(lower, ".txt") {
		return -1
	}
	score := 0
	for _, alias := range osAliases(runtime.GOOS) {
		if strings.Contains(lower, alias) {
			score += 5
			break
		}
	}
	for _, alias := range archAliases(runtime.GOARCH) {
		if strings.Contains(lower, alias) {
			score += 5
			break
		}
	}
	if strings.Contains(lower, "tdl") {
		score += 2
	}
	if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		score += 2
	}
	if runtime.GOOS == goosWindows && strings.Contains(lower, ".exe") {
		score++
	}
	return score
}

func archAliases(arch string) []string {
	switch arch {
	case "amd64":
		return []string{"amd64", "x86_64", "x64"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	case "386":
		return []string{"386", "i386", "x86"}
	default:
		return []string{arch}
	}
}

func osAliases(goos string) []string {
	switch goos {
	case goosWindows:
		return []string{"windows", "win"}
	case "darwin":
		return []string{"darwin", "macos", "osx"}
	default:
		return []string{goos}
	}
}

func needsUpdate(current, latest string) bool {
	current = strings.TrimSpace(current)
	latest = strings.TrimSpace(latest)
	if latest == "" {
		return false
	}
	if current == "" || strings.EqualFold(current, "dev") || strings.EqualFold(current, "unknown") {
		return true
	}
	currentSemver := canonicalVersion(current)
	latestSemver := canonicalVersion(latest)
	if currentSemver != "" && latestSemver != "" {
		return semver.Compare(currentSemver, latestSemver) < 0
	}
	return strings.TrimPrefix(current, "v") != strings.TrimPrefix(latest, "v")
}

func canonicalVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return semver.Canonical(version)
}

func extractExecutable(assetPath, assetName, dir string) (string, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipExecutable(assetPath, dir)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGzExecutable(assetPath, dir)
	default:
		return assetPath, nil
	}
}

func extractZipExecutable(assetPath, dir string) (string, error) {
	reader, err := zip.OpenReader(assetPath)
	if err != nil {
		return "", errors.Wrap(err, "open update zip")
	}
	defer reader.Close()

	var chosen *zip.File
	bestScore := -1
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		score := executableEntryScore(file.Name)
		if score > bestScore {
			bestScore = score
			chosen = file
		}
	}
	if chosen == nil || bestScore <= 0 {
		return "", errors.New("update zip does not contain a tdl executable")
	}

	src, err := chosen.Open()
	if err != nil {
		return "", errors.Wrap(err, "open executable in zip")
	}
	defer src.Close()

	dest := filepath.Join(dir, executableFileName())
	return writeExecutable(dest, src)
}

func extractTarGzExecutable(assetPath, dir string) (string, error) {
	file, err := os.Open(assetPath)
	if err != nil {
		return "", errors.Wrap(err, "open update archive")
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", errors.Wrap(err, "open gzip stream")
	}
	defer gz.Close()

	type candidate struct {
		name string
		data []byte
	}
	var chosen candidate
	bestScore := -1
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.Wrap(err, "read tar archive")
		}
		if header.FileInfo().IsDir() {
			continue
		}
		score := executableEntryScore(header.Name)
		if score <= bestScore {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return "", errors.Wrap(err, "read executable from tar")
		}
		chosen = candidate{name: header.Name, data: data}
		bestScore = score
	}
	if chosen.name == "" || bestScore <= 0 {
		return "", errors.New("update archive does not contain a tdl executable")
	}
	dest := filepath.Join(dir, executableFileName())
	return writeExecutable(dest, bytes.NewReader(chosen.data))
}

func executableEntryScore(name string) int {
	base := strings.ToLower(filepath.Base(name))
	score := 0
	if base == executableFileName() {
		score += 10
	}
	if strings.Contains(base, "tdl") {
		score += 5
	}
	if runtime.GOOS == goosWindows {
		if strings.HasSuffix(base, ".exe") {
			score += 4
		} else {
			return -1
		}
	}
	if strings.Contains(base, "sha") || strings.Contains(base, "readme") || strings.Contains(base, "license") {
		return -1
	}
	return score
}

func writeExecutable(dest string, src io.Reader) (string, error) {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", errors.Wrap(err, "create extracted executable")
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return "", errors.Wrap(err, "write extracted executable")
	}
	return dest, nil
}

func executableFileName() string {
	if runtime.GOOS == goosWindows {
		return "tdl.exe"
	}
	return "tdl"
}

func copyCurrentExecutableToTemp() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", errors.Wrap(err, "get executable path")
	}
	src, err := os.Open(exe)
	if err != nil {
		return "", errors.Wrap(err, "open executable")
	}
	defer src.Close()

	helper := filepath.Join(os.TempDir(), fmt.Sprintf("tdl-update-helper-%d%s", time.Now().UnixNano(), filepath.Ext(exe)))
	dst, err := os.OpenFile(helper, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", errors.Wrap(err, "create update helper")
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return "", errors.Wrap(err, "copy update helper")
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	return helper, nil
}

func replaceExecutable(source, target string) error {
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "backup current executable")
	}
	if err := os.Rename(source, target); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		return errors.Wrap(err, "install update executable")
	}
	_ = os.Chmod(target, 0o755)
	_ = os.Remove(backup)
	return nil
}

func parseApplyArgs(args []string) (source, target string, pid int32, cwd string, runArgs []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			runArgs = append([]string{}, args[i+1:]...)
			break
		}
		if i+1 >= len(args) {
			err = fmt.Errorf("missing value for %s", args[i])
			return source, target, pid, cwd, runArgs, err
		}
		value := args[i+1]
		switch args[i] {
		case "--source":
			source = value
		case "--target":
			target = value
		case "--pid":
			var parsed int
			_, err = fmt.Sscanf(value, "%d", &parsed)
			pid = int32(parsed)
		case "--cwd":
			cwd = value
		default:
			err = fmt.Errorf("unknown update helper argument %q", args[i])
			return source, target, pid, cwd, runArgs, err
		}
		i++
	}
	if source == "" || target == "" {
		err = errors.New("update source and target are required")
	}
	return source, target, pid, cwd, runArgs, err
}

func waitForPIDExit(ctx context.Context, pid int32, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		exists, err := process.PidExistsWithContext(ctx, pid)
		if err != nil {
			return errors.Wrap(err, "check parent process")
		}
		if !exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func safeFileName(name string) string {
	name = filepath.Base(name)
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name)
	if name == "." || name == "" {
		return "tdl-update"
	}
	return name
}
