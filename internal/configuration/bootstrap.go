// Package configuration wires the configuration SWC to filesystem and legacy
// adapters. Only this migration boundary knows both document formats.
package configuration

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/snakexgc/tdl/application"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/componentconfig"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

// Open gives tdl_config.json unconditional precedence. Legacy inputs are read
// once, only when the unified file does not exist, and are never modified.
func Open(ctx context.Context, home, componentDirectory string) (*manager.Service, error) {
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
	bootstrap := legacy.DefaultConfig()
	hasLegacy := false
	legacyPath := filepath.Join(home, "config.json")
	if _, err := os.Stat(legacyPath); err == nil {
		hasLegacy = true
		bootstrap, err = legacy.Load(legacyPath)
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	doc := manager.Document{
		Version: manager.Version,
		System:  ports.SystemConfiguration{Namespace: bootstrap.Namespace, Debug: bootstrap.Debug},
	}
	if hasLegacy {
		exported, err := componentconfig.Export(bootstrap, catalog)
		if err != nil {
			return nil, err
		}
		doc.Components = convert(exported)
	}
	// Only the selected session's legacy settings become the global settings.
	// Inactive session directories are left untouched and never validated.
	directory := componentDirectory
	if directory == "" {
		directory = filepath.Join(home, "components", base64.RawURLEncoding.EncodeToString([]byte(bootstrap.Namespace)))
	}
	if info, err := os.Stat(directory); err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("legacy component configuration path is not a directory")
		}
		store := config.NewStore(directory)
		documents := map[string]config.Document{}
		for _, definition := range catalog.Definitions() {
			id := definition.Manifest.ID
			if id == manager.ID {
				continue
			}
			document, err := store.Load(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("import %s: %w", id, err)
			}
			documents[id] = document
		}
		doc.Components = convert(documents)
	} else if componentDirectory != "" || !os.IsNotExist(err) {
		return nil, err
	}
	if err := service.Create(ctx, doc); err != nil {
		return nil, fmt.Errorf("create %s: %w", manager.Filename, err)
	}
	return service, nil
}

func convert(documents map[string]config.Document) map[string]manager.Component {
	result := map[string]manager.Component{}
	for id, doc := range documents {
		if id != manager.ID {
			result[id] = manager.Component{Enabled: doc.Enabled, Values: doc.Values}
		}
	}
	return result
}

// Install supplies old transport DTOs without giving legacy code another file
// to write. System settings and account selection also persist through the SWC.
func Install(ctx context.Context, service *manager.Service) (*config.Store, error) {
	system, err := service.System(ctx)
	if err != nil {
		return nil, err
	}
	store := service.Store()
	bootstrap := legacy.DefaultConfig()
	bootstrap.Namespace, bootstrap.Debug = system.Namespace, system.Debug
	effective, _, err := componentconfig.Load(ctx, store, bootstrap)
	if err != nil {
		return nil, err
	}
	legacy.Install(effective, func(ctx context.Context, before, next *legacy.Config) error {
		copy, err := legacy.Clone(next)
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
