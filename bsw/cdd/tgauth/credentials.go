// Package tgauth owns session identity metadata and compatible session commits.
package tgauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const FingerprintKey = "account.credentials.fingerprint"

var ErrNeedsRelogin = errors.New("telegram API credentials changed; log in again or restore the previous credentials")

func Fingerprint(app types.TelegramApp) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", app.AppID, app.AppHash)))
	return hex.EncodeToString(sum[:])
}

// ValidateCredentials is read-only: changing configuration never deletes or
// rewrites an existing session. Legacy sessions derive identity from their app
// marker until their next successful login commits explicit metadata.
func ValidateCredentials(ctx context.Context, store storage.Storage, selected, legacy types.TelegramApp) error {
	if _, err := store.Get(ctx, "session"); errors.Is(err, storage.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	identity, err := store.Get(ctx, FingerprintKey)
	if errors.Is(err, storage.ErrNotFound) {
		identity = []byte(Fingerprint(legacy))
	} else if err != nil {
		return err
	}
	if string(identity) != Fingerprint(selected) {
		return ErrNeedsRelogin
	}
	return nil
}

func CommitSession(ctx context.Context, store storage.Storage, session []byte, preset, fingerprint string) error {
	if len(session) == 0 || preset == "" || fingerprint == "" {
		return errors.New("incomplete authenticated session")
	}
	return storage.Update(ctx, store, func(tx storage.Storage) error {
		if err := tx.Set(ctx, "session", session); err != nil {
			return err
		}
		if err := tx.Set(ctx, "app", []byte(preset)); err != nil {
			return err
		}
		return tx.Set(ctx, FingerprintKey, []byte(fingerprint))
	})
}
