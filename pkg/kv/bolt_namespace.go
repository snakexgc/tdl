package kv

import (
	"bytes"
	"context"
	"time"

	"go.etcd.io/bbolt"

	"github.com/snakexgc/tdl/internal/core/storage"
)

var boltOptions = &bbolt.Options{
	Timeout:      time.Second,
	NoGrowSync:   false,
	FreelistType: bbolt.FreelistArrayType,
}

type boltNamespace struct {
	db *bbolt.DB
	ns []byte
}

func (l *boltNamespace) Get(_ context.Context, key string) ([]byte, error) {
	var val []byte

	if err := l.db.View(func(tx *bbolt.Tx) error {
		val = bytes.Clone(tx.Bucket(l.ns).Get([]byte(key)))
		return nil
	}); err != nil {
		return nil, err
	}

	if val == nil {
		return nil, storage.ErrNotFound
	}
	return val, nil
}

func (l *boltNamespace) Set(_ context.Context, key string, value []byte) error {
	return l.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(l.ns).Put([]byte(key), value)
	})
}

func (l *boltNamespace) Delete(_ context.Context, key string) error {
	return l.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(l.ns).Delete([]byte(key))
	})
}

// Snapshot copies one namespace without opening other accounts or workspace files.
func (b *bolt) Snapshot(ctx context.Context, namespace string) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, err := b.open(namespace)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte)
	err = store.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(store.ns).ForEach(func(key, value []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			result[string(key)] = bytes.Clone(value)
			return nil
		})
	})
	return result, err
}
