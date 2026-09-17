package taskhub

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/snakexgc/tdl/internal/core/storage"
)

// DeleteSnapshotKey supports the legacy administrative cleanup action without
// deleting a record changed since the maintenance snapshot was taken. Task
// indexes are repaired in the same transaction as their records.
func DeleteSnapshotKey(ctx context.Context, s storage.Storage, key string, expected []byte) (bool, error) {
	deleted := false
	err := storage.Update(ctx, s, func(tx storage.Storage) error {
		current, err := tx.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(current, expected) {
			return nil
		}
		for _, collection := range []*Collection{Links(tx), Aria2(tx), Local(tx), Forward(tx)} {
			if key == collection.indexKey {
				// Record deletion repairs indexes; deleting a whole index can hide
				// concurrently created records, so leave it to the owning repository.
				return nil
			}
			if strings.HasPrefix(key, collection.prefix) {
				index, err := collection.index(ctx, tx)
				if err != nil {
					return err
				}
				delete(index, strings.TrimPrefix(key, collection.prefix))
				if err := collection.saveIndex(ctx, tx, index); err != nil {
					return err
				}
				break
			}
		}
		if err := tx.Delete(ctx, key); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted && err == nil, err
}
