// Package tgauth owns session identity metadata and atomic session commits.
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
// rewrites an existing session. Existing sessions must include credential identity.
func ValidateCredentials(ctx context.Context, store storage.Storage, selected types.TelegramApp) error {
	if _, err := store.Get(ctx, SessionKey); errors.Is(err, storage.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	identity, err := store.Get(ctx, FingerprintKey)
	if errors.Is(err, storage.ErrNotFound) {
		return ErrNeedsRelogin
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
	scoped, err := SessionStore(store)
	if err != nil {
		return err
	}
	return storage.Update(ctx, scoped, func(tx storage.Storage) error {
		if err := tx.Set(ctx, SessionKey, session); err != nil {
			return err
		}
		if err := tx.Set(ctx, AppKey, []byte(preset)); err != nil {
			return err
		}
		return tx.Set(ctx, FingerprintKey, []byte(fingerprint))
	})
}
