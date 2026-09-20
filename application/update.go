package application

import (
	"context"
	"fmt"

	account "github.com/snakexgc/tdl/application/account.telegram"
	updater "github.com/snakexgc/tdl/application/update.self"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const DefaultUpdateRepository = updater.DefaultRepository

const sharedProxyField = "proxy"

// Process helpers remain in the composition boundary because the apply helper
// also runs before the normal application runtime has been initialized.
func CheckUpdate(ctx context.Context, repository, proxy string) (types.UpdateInfo, error) {
	if repository != DefaultUpdateRepository {
		return updater.CheckLatestFrom(ctx, repository, proxy)
	}
	service, stop, err := updatePort(ctx, proxy)
	if err != nil {
		return types.UpdateInfo{}, err
	}
	defer stop()
	return service.Check(ctx)
}

func DownloadUpdate(ctx context.Context, proxy string) (types.UpdatePlan, types.UpdateInfo, error) {
	service, stop, err := updatePort(ctx, proxy)
	if err != nil {
		return types.UpdatePlan{}, types.UpdateInfo{}, err
	}
	defer stop()
	return service.Download(ctx)
}

func updatePort(ctx context.Context, proxy string) (ports.Updater, func(), error) {
	registry, err := Registry()
	if err != nil {
		return nil, nil, err
	}
	host, err := registry.Build(types.DefaultAccount, map[string]bool{account.ID: true, updater.ID: true}, map[string]map[string]any{account.ID: {sharedProxyField: proxy}})
	if err != nil {
		return nil, nil, err
	}
	stop := func() { _ = host.Stop(ctx) }
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			stop()
			return nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.UpdaterName)
	if err != nil {
		stop()
		return nil, nil, err
	}
	return value.(ports.Updater), stop, nil
}

func StartUpdateApply(plan types.UpdatePlan, target string, args []string) error {
	return updater.StartApply(plan, target, args)
}
func RunUpdateApply(args []string) error { return updater.RunApply(args) }
func StartAttached(path string, args []string, cwd string) error {
	return updater.StartAttached(path, args, cwd)
}
