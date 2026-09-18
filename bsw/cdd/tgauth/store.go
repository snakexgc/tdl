package tgauth

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/bsw/services/nvm"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const (
	sessionDataset = "account.telegram.session"
	SessionKey     = "session"
	AppKey         = "app"
)

// SessionStore limits the protocol adapter to the three existing session keys.
// It preserves the underlying driver's transactions and namespace isolation.
func SessionStore(store storage.Storage) (storage.Storage, error) {
	registry := nvm.New(store)
	if err := registry.Register(nvm.Dataset{Name: sessionDataset, Writer: "tgauth", Keys: []string{SessionKey, AppKey, FingerprintKey}}); err != nil {
		return nil, err
	}
	return registry.Writer(sessionDataset, "tgauth")
}

// DeleteSession removes identity and session bytes together. Failure does not
// leave a fingerprint belonging to an already deleted session on transactional drivers.
func DeleteSession(ctx context.Context, store storage.Storage) (int, error) {
	scoped, err := SessionStore(store)
	if err != nil {
		return 0, err
	}
	deleted := 0
	err = storage.Update(ctx, scoped, func(tx storage.Storage) error {
		for _, key := range []string{SessionKey, AppKey, FingerprintKey} {
			if _, err := tx.Get(ctx, key); errors.Is(err, storage.ErrNotFound) {
				continue
			} else if err != nil {
				return err
			}
			if err := tx.Delete(ctx, key); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
