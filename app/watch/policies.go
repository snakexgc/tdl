package watch

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const (
	filterComponentID = "filter.rules"
	namingComponentID = "naming.rules"
)

// startPolicies adapts the legacy config to component-owned configuration.
func startPolicies(ctx context.Context, account string, opts Options) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
	registry, err := application.Registry()
	if err != nil {
		return nil, nil, nil, err
	}
	if account == "" {
		account = string(types.DefaultAccount)
	}
	runtime, err := registry.Build(types.AccountID(account), map[string]bool{filterComponentID: true, namingComponentID: true}, PolicyValues(opts))
	if err != nil {
		return nil, nil, nil, err
	}
	for _, status := range runtime.Start(ctx) {
		if status.State != rte.Running {
			_ = runtime.Stop(ctx)
			return nil, nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := runtime.Resolve(ports.FilterRulesName)
	if err != nil {
		_ = runtime.Stop(ctx)
		return nil, nil, nil, err
	}
	naming, err := runtime.Resolve(ports.NamingRulesName)
	if err != nil {
		_ = runtime.Stop(ctx)
		return nil, nil, nil, err
	}
	return runtime, value.(ports.FilterRules), naming.(ports.NamingRules), nil
}

func namingConfig(opts Options) map[string]any {
	maximum := opts.FilenameMaxLength
	if maximum <= 0 || maximum > 255 {
		maximum = 255
	}
	return map[string]any{"filename": opts.Template, "directory": opts.Dir, "max_bytes": maximum}
}

func filterConfig(opts Options) map[string]any {
	include, exclude := opts.Include, opts.Exclude
	if include == nil {
		include = []string{}
	}
	if exclude == nil {
		exclude = []string{}
	}
	return map[string]any{"include": include, "exclude": exclude, "min_mb": opts.FileSizeMinMB, "max_mb": opts.FileSizeMaxMB}
}

// PolicyValues adapts legacy options for the owning production RTE host.
func PolicyValues(opts Options) map[string]map[string]any {
	return map[string]map[string]any{
		filterComponentID: filterConfig(opts),
		namingComponentID: namingConfig(opts),
	}
}
