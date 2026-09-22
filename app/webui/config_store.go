package webui

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/ports"
)

type configurationStore struct {
	manager ports.ConfigurationManager
	active  ports.SystemConfiguration
}

func (s configurationStore) Read(ctx context.Context) (ports.SystemConfiguration, error) {
	if s.manager == nil {
		return s.active, ctx.Err() // Standalone read-only preview.
	}
	return s.manager.System(ctx)
}

func (s configurationStore) Save(ctx context.Context, before, next ports.SystemConfiguration) error {
	if s.manager == nil {
		return fmt.Errorf("system configuration repository is unavailable")
	}
	return s.manager.SetSystem(ctx, before, next)
}
