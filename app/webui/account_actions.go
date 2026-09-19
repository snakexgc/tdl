package webui

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/pkg/config"
)

type accountSelection struct{}

func (accountSelection) Current(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg := config.Get()
	if cfg == nil {
		return "", fmt.Errorf("configuration is not initialized")
	}
	return cfg.Namespace, nil
}

func (accountSelection) Select(ctx context.Context, expected, target string) error {
	_, err := config.SelectNamespace(ctx, expected, target)
	return err
}
