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
	return manifest.Manifest{
		ID: ID, Pages: []manifest.Page{{Path: "/downloads", Title: "下载管理"}}, Title: "下载任务控制",
		Provides: []manifest.Port{manifest.PortOf[ports.DownloadControl](ports.DownloadControlName, 1, 0), manifest.PortOf[ports.DownloadRouting](ports.DownloadRoutingName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: executorsField, Title: "执行器优先级（local、aria2、http；空列表沿用旧模式）", Type: manifest.Strings, Default: []string{}},
			{Name: "local_root", Title: "显式本地执行器的绝对根目录", Type: manifest.String, Default: ""},
		},
	}
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
	if seen[localExecutor] && !filepath.IsAbs(route.LocalRoot) {
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
