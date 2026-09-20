package watch

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

// startPolicies assembles explicit test policies.
func startPolicies(ctx context.Context, account string, opts Options) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
	registry, err := application.Registry()
	if err != nil {
		return nil, nil, nil, err
	}
	if account == "" {
		account = string(types.DefaultAccount)
	}
	runtime, err := registry.Build(types.AccountID(account), map[string]bool{filterComponentID: true, namingComponentID: true}, policyValues(opts))
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

func controllerTestOptions(t *testing.T, opts Options) Options {
	t.Helper()
	host, filter, naming, err := startPolicies(context.Background(), "default", opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	opts.Filter, opts.Naming = filter, naming
	return opts
}
