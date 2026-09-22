package storage

import (
	"context"
	"errors"
)

// Transactional stores commit all writes only if fn succeeds. The callback must
// use the supplied storage and must not retain it or start another transaction.
type Transactional interface {
	Update(context.Context, func(Storage) error) error
}

func Update(ctx context.Context, s Storage, fn func(Storage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transactional, ok := s.(Transactional); ok {
		return transactional.Update(ctx, fn)
	}
	return errors.New("storage does not support atomic updates")
}
