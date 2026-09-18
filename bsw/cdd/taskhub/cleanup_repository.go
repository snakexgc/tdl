package taskhub

import (
	"context"
	"errors"
	"strings"

	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

type CleanupRepository struct {
	Engine    kv.Storage
	Namespace string
	Store     storage.Storage
}

func protectedCleanupKey(key string) bool {
	if key == tgauth.SessionKey || key == tgauth.AppKey || key == tgauth.FingerprintKey {
		return true
	}
	for _, prefix := range []string{"peers:", "state:", "chan:", "access_hash:"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (r CleanupRepository) Snapshot(ctx context.Context) (ports.CleanupSnapshot, error) {
	result := ports.CleanupSnapshot{Records: map[string][]byte{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if r.Engine == nil || r.Store == nil || r.Namespace == "" {
		return result, errors.New("namespace storage is not configured")
	}
	meta, err := r.Engine.MigrateTo()
	if err != nil {
		return result, err
	}
	for key, value := range meta[r.Namespace] {
		if protectedCleanupKey(key) {
			result.Protected++
			continue
		}
		result.Records[key] = append([]byte(nil), value...)
	}
	return result, ctx.Err()
}

func (r CleanupRepository) DeleteUnchanged(ctx context.Context, key string, expected []byte) (bool, error) {
	if protectedCleanupKey(key) {
		return false, errors.New("protected account data cannot be removed by maintenance")
	}
	if r.Store == nil {
		return false, errors.New("namespace storage is not configured")
	}
	return DeleteSnapshotKey(ctx, r.Store, key, expected)
}
