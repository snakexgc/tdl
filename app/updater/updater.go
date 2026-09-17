// Package updater preserves the legacy process/control adapter.
package updater

import (
	"context"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
)

type (
	Info = types.UpdateInfo
	Plan = types.UpdatePlan
)

const DefaultRepository = application.DefaultUpdateRepository

func CheckLatest(ctx context.Context, proxyURL string) (Info, error) {
	return CheckLatestFrom(ctx, DefaultRepository, proxyURL)
}

func CheckLatestFrom(ctx context.Context, repository, proxyURL string) (Info, error) {
	return application.CheckUpdate(ctx, repository, proxyURL)
}

func DownloadLatest(ctx context.Context, proxyURL string) (Plan, Info, error) {
	return application.DownloadUpdate(ctx, proxyURL)
}

func StartApply(plan Plan, targetPath string, args []string) error {
	return application.StartUpdateApply(plan, targetPath, args)
}
func RunApply(args []string) error { return application.RunUpdateApply(args) }
func StartAttached(path string, args []string, cwd string) error {
	return application.StartAttached(path, args, cwd)
}
