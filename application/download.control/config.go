package downloadcontrol

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	executorsField = "executors"
	localRootField = "local_root"
	aria2Executor  = "aria2"
	httpExecutor   = "http"
)

func Manifest() manifest.Manifest {
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "download", Title: "下载管理", Order: 10, SettingsURL: "/config?tab=download"},
		ID:      ID, Commands: Commands(), Pages: []manifest.Page{{Path: "/downloads", Title: "下载管理", View: "downloads", Module: "/static/js/downloads.js", Style: "/static/css/downloads.css", Order: 20, KeepVisible: true, SettingsURL: "/config?tab=download"}}, Title: "下载任务控制",
		Provides: []manifest.Port{manifest.PortOf[ports.DownloadControl](ports.DownloadControlName, 2, 0), manifest.PortOf[ports.DownloadRouting](ports.DownloadRoutingName, 1, 0), manifest.PortOf[ports.DownloadPipeline](ports.DownloadPipelineName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: executorsField, Title: "下载器", Help: "选择本地下载器或 aria2。两种方式均提供 HTTP 下载链接，可复制到其他下载工具使用。", Type: manifest.Strings, Default: []string{aria2Executor, httpExecutor}, Editor: "/static/js/download-methods.js"},
			{Name: localRootField, Title: "本地保存目录", Help: "留空使用 TDL 可执行文件所在目录下的 download 文件夹，下载时自动创建。也可填写本机绝对路径，例如 D:\\Downloads 或 /data/downloads；容器部署时填写容器内路径。", Type: manifest.String, Default: ""},
		},
	}, "download", "下载方式").SettingsOrder(10)
}

func (s *Service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var route ports.DownloadRoute
	if err := view.Get(executorsField, &route.Executors); err != nil {
		return nil, err
	}
	if err := view.Get(localRootField, &route.LocalRoot); err != nil {
		return nil, err
	}
	if len(route.Executors) == 0 {
		return nil, fmt.Errorf("executors cannot be empty")
	}
	seen := map[string]bool{}
	for index, name := range route.Executors {
		if !slices.Contains([]string{localExecutor, aria2Executor, httpExecutor}, name) || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate download executor %q", name)
		}
		if name == httpExecutor && index != len(route.Executors)-1 {
			return nil, fmt.Errorf("http must be the final executor")
		}
		seen[name] = true
	}
	// Older documents allowed multiple downloaders. Preserve the first choice
	// and the link fallback, without starting or submitting to another downloader.
	selected := []string{route.Executors[0]}
	if selected[0] != httpExecutor && seen[httpExecutor] {
		selected = append(selected, httpExecutor)
	}
	route.Executors = selected
	route.LocalRoot = strings.TrimSpace(route.LocalRoot)
	if route.LocalRoot != "" && !filepath.IsAbs(route.LocalRoot) {
		return nil, fmt.Errorf("local_root must be empty or an absolute local path")
	}
	return func() { s.route.Store(&route) }, nil
}

func (s *Service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (s *Service) Route(ctx context.Context, account types.AccountID) (ports.DownloadRoute, error) {
	ctx, done, err := s.begin(ctx)
	if err != nil {
		return ports.DownloadRoute{}, err
	}
	defer done()
	if account != s.account {
		return ports.DownloadRoute{}, fmt.Errorf("download account mismatch")
	}
	if err := ctx.Err(); err != nil {
		return ports.DownloadRoute{}, err
	}
	if route := s.route.Load(); route != nil {
		return ports.DownloadRoute{Executors: slices.Clone(route.Executors), LocalRoot: route.LocalRoot}, nil
	}
	return ports.DownloadRoute{}, nil
}

// ValidateConfiguration validates offline edits without acquiring resources.
func ValidateConfiguration(ctx context.Context, view config.View) error {
	_, err := (&Service{}).PrepareConfig(ctx, view)
	return err
}
