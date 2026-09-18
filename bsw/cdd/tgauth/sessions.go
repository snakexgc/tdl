package tgauth

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
)

type SessionRepository struct {
	Engine      kv.Storage
	Connections *Connections
}

func (s SessionRepository) List(ctx context.Context) ([]string, error) {
	if s.Engine == nil {
		return nil, errors.New("kv engine is not configured")
	}
	names, err := s.Engine.Namespaces()
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		store, err := s.Engine.Open(name)
		if err != nil {
			continue
		}
		data, err := store.Get(ctx, "session")
		if err == nil && len(data) > 0 {
			result = append(result, name)
		}
	}
	return result, nil
}

func (s SessionRepository) Delete(ctx context.Context, name string) (int, error) {
	if s.Engine == nil {
		return 0, errors.New("kv engine is not configured")
	}
	var deleted int
	err := s.Connections.ReplaceIdle(ctx, types.AccountID(name), func() error {
		store, err := s.Engine.Open(name)
		if err != nil {
			return err
		}
		data, err := store.Get(ctx, "session")
		if err != nil || len(data) == 0 {
			return errors.New("请选择已有登录用户。")
		}
		deleted, err = DeleteSession(ctx, store)
		return err
	})
	return deleted, err
}
