package downloadcontrol

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	executorsField = "executors"
	aria2Executor  = "aria2"
	httpExecutor   = "http"
)

func Manifest() manifest.Manifest {
	return manifest.WithSettings(manifest.Manifest{
		Feature: manifest.Feature{ID: "download", Title: "下载管理", Order: 10, SettingsURL: "/config?tab=download"},
		ID:      ID, Commands: Commands(), Pages: []manifest.Page{{Path: "/downloads", Title: "下载管理", View: "downloads", Module: "/static/js/downloads.js", Style: "/static/css/downloads.css", Order: 20, KeepVisible: true, SettingsURL: "/config?tab=download"}}, Title: "下载任务控制",
		Provides: []manifest.Port{manifest.PortOf[ports.DownloadControl](ports.DownloadControlName, 2, 0), manifest.PortOf[ports.DownloadRouting](ports.DownloadRoutingName, 1, 0), manifest.PortOf[ports.DownloadPipeline](ports.DownloadPipelineName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: executorsField, Title: "执行器优先级", Help: "每行一个，按顺序尝试：local、aria2 或 http。至少填写一个；http 仅生成链接，必须放在最后。", Type: manifest.Strings, Default: []string{aria2Executor, httpExecutor}},
			{Name: "local_root", Title: "本地下载根目录", Help: "使用本地执行器时必须填写本机绝对路径。", Type: manifest.String, Default: ""},
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
	if err := view.Get("local_root", &route.LocalRoot); err != nil {
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
	if (seen[localExecutor] || route.LocalRoot != "") && !filepath.IsAbs(route.LocalRoot) {
		return nil, fmt.Errorf("local_root must be an absolute local path when local is selected")
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
