package kv

import (
	"bytes"
	"context"

	"go.etcd.io/bbolt"

	"github.com/snakexgc/tdl/internal/core/storage"
)

func (l *legacyKV) Update(ctx context.Context, fn func(storage.Storage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return l.db.Update(func(tx *bbolt.Tx) error {
		if err := fn(&bucketStorage{bucket: tx.Bucket(l.ns)}); err != nil {
			return err
		}
		return ctx.Err()
	})
}

type bucketStorage struct{ bucket *bbolt.Bucket }

func (s *bucketStorage) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := s.bucket.Get([]byte(key))
	if value == nil {
		return nil, storage.ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (s *bucketStorage) Set(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.bucket.Put([]byte(key), value)
}

func (s *bucketStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.bucket.Delete([]byte(key))
}

func (f *fileKV) Update(ctx context.Context, fn func(storage.Storage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.f.mutate(func(m map[string]map[string][]byte) error {
		if m[f.ns] == nil {
			m[f.ns] = make(map[string][]byte)
		}
		if err := fn(mapStorage(m[f.ns])); err != nil {
			return err
		}
		return ctx.Err()
	})
}

type mapStorage map[string][]byte

func (s mapStorage) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := s[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (s mapStorage) Set(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s[key] = bytes.Clone(value)
	return nil
}

func (s mapStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s, key)
	return nil
}
