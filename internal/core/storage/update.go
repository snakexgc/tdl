package storage

import (
	"context"
	"sync"
)

// Transactional stores commit all writes only if fn succeeds. The callback must
// use the supplied storage and must not retain it or start another transaction.
type Transactional interface {
	Update(context.Context, func(Storage) error) error
}

// compatibilityUpdates serializes callers using older/custom Storage adapters.
// It cannot provide crash recovery or rollback for those adapters.
var compatibilityUpdates sync.Mutex

func Update(ctx context.Context, s Storage, fn func(Storage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transactional, ok := s.(Transactional); ok {
		return transactional.Update(ctx, fn)
	}
	compatibilityUpdates.Lock()
	defer compatibilityUpdates.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(s)
}
