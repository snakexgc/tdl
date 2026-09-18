package webui

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
)

type accountSelection struct{}

func (accountSelection) Current(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return config.Get().Namespace, nil
}

func (accountSelection) Select(ctx context.Context, expected, target string) error {
	before, err := config.Clone(config.Get())
	if err != nil {
		return err
	}
	if before.Namespace != expected {
		return ports.ErrConfigurationConflict
	}
	next, err := config.Clone(before)
	if err != nil {
		return err
	}
	next.Namespace = target
	return config.CompareAndSet(ctx, before, next)
}
