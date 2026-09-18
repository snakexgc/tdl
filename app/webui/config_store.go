package webui

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

type configurationStore struct{ saved func(*config.Config) }

func (configurationStore) Read(ctx context.Context) (*types.RuntimeConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return config.Clone(config.Get())
}
func (configurationStore) Validate(value *types.RuntimeConfig) error { return config.Validate(value) }
func (s configurationStore) Save(ctx context.Context, before, next *types.RuntimeConfig) error {
	if err := config.CompareAndSet(ctx, before, next); err != nil {
		return err
	}
	if s.saved != nil {
		s.saved(next)
	}
	return nil
}
