// Package configuration wires the configuration SWC to the filesystem and
// supplies the runtime snapshot used by transport services.
package configuration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/snakexgc/tdl/application"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/componentconfig"
	runtimeconfig "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

// Open loads the unified configuration or creates the current default document.
func Open(ctx context.Context, home string) (*manager.Service, error) {
	catalog, err := application.Catalog()
	if err != nil {
		return nil, err
	}
	service := manager.New(config.File{Path: filepath.Join(home, manager.Filename)}, catalog)
	if _, err := service.System(ctx); err == nil {
		return service, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	doc, err := service.Defaults(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.Create(ctx, doc); err != nil {
		return nil, fmt.Errorf("create %s: %w", manager.Filename, err)
	}
	return service, nil
}

// Install publishes runtime settings and persists system changes through the SWC.
func Install(ctx context.Context, service *manager.Service) (*config.Store, error) {
	system, err := service.System(ctx)
	if err != nil {
		return nil, err
	}
	store := service.Store()
	bootstrap := runtimeconfig.DefaultConfig()
	bootstrap.Namespace, bootstrap.Debug = system.Namespace, system.Debug
	effective, _, err := componentconfig.Load(ctx, store, bootstrap)
	if err != nil {
		return nil, err
	}
	runtimeconfig.Install(effective, func(ctx context.Context, before, next *runtimeconfig.Config) error {
		copy, err := runtimeconfig.Clone(next)
		if err != nil {
			return err
		}
		copy.Namespace, copy.Debug = before.Namespace, before.Debug
		if !reflect.DeepEqual(copy, before) {
			return fmt.Errorf("business configuration must be saved through the component configuration port")
		}
		return service.SetSystem(ctx,
			ports.SystemConfiguration{Namespace: before.Namespace, Debug: before.Debug},
			ports.SystemConfiguration{Namespace: next.Namespace, Debug: next.Debug})
	})
	return store, nil
}
