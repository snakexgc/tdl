package webui

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
)

type configurationStore struct{ saved func(*config.Config) }

func (configurationStore) Read(ctx context.Context) (ports.SystemConfiguration, error) {
	if err := ctx.Err(); err != nil {
		return ports.SystemConfiguration{}, err
	}
	cfg := config.Get()
	return ports.SystemConfiguration{Namespace: cfg.Namespace, Debug: cfg.Debug}, nil
}

func (s configurationStore) Save(ctx context.Context, before, next ports.SystemConfiguration) error {
	current, err := config.Clone(config.Get())
	if err != nil {
		return err
	}
	if current.Namespace != before.Namespace || current.Debug != before.Debug {
		return ports.ErrConfigurationConflict
	}
	updated, err := config.Clone(current)
	if err != nil {
		return err
	}
	updated.Namespace, updated.Debug = next.Namespace, next.Debug
	if err := config.CompareAndSet(ctx, current, updated); err != nil {
		return err
	}
	if s.saved != nil {
		s.saved(updated)
	}
	return nil
}
