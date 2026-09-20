// Package taskhub owns indexed task persistence. Storage namespaces
// define the account boundary; each repository validates its record schema.
package taskhub

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/snakexgc/tdl/internal/core/storage"
)

type Collection struct {
	initErr  error
	store    storage.Storage
	prefix   string
	indexKey string
}

func New(store storage.Storage, prefix, indexKey string) *Collection {
	return &Collection{store: store, prefix: prefix, indexKey: indexKey}
}

func (c *Collection) Put(ctx context.Context, id string, data []byte, createdAt time.Time) error {
	if c.initErr != nil {
		return c.initErr
	}
	if id == "" || !json.Valid(data) {
		return errors.New("invalid task id or JSON record")
	}
	return storage.Update(ctx, c.store, func(tx storage.Storage) error {
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		if err := tx.Set(ctx, c.prefix+id, data); err != nil {
			return err
		}
		index[id] = createdAt
		return c.saveIndex(ctx, tx, index)
	})
}

func (c *Collection) Get(ctx context.Context, id string) ([]byte, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	return c.store.Get(ctx, c.prefix+id)
}

func (c *Collection) Remove(ctx context.Context, id string) error {
	if c.initErr != nil {
		return c.initErr
	}
	return storage.Update(ctx, c.store, func(tx storage.Storage) error {
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		if err := tx.Delete(ctx, c.prefix+id); err != nil {
			return err
		}
		delete(index, id)
		return c.saveIndex(ctx, tx, index)
	})
}

// Records returns a consistent snapshot and repairs dangling index entries.
func (c *Collection) Records(ctx context.Context) (map[string][]byte, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	result := make(map[string][]byte)
	err := storage.Update(ctx, c.store, func(tx storage.Storage) error {
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		changed := false
		for id := range index {
			data, err := tx.Get(ctx, c.prefix+id)
			if errors.Is(err, storage.ErrNotFound) {
				delete(index, id)
				changed = true
				continue
			}
			if err != nil {
				return err
			}
			result[id] = data
		}
		if changed {
			return c.saveIndex(ctx, tx, index)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Collection) index(ctx context.Context, tx storage.Storage) (map[string]time.Time, error) {
	data, err := tx.Get(ctx, c.indexKey)
	if errors.Is(err, storage.ErrNotFound) {
		return make(map[string]time.Time), nil
	}
	if err != nil {
		return nil, err
	}
	index := make(map[string]time.Time)
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]time.Time)
	}
	return index, nil
}

func (c *Collection) saveIndex(ctx context.Context, tx storage.Storage, index map[string]time.Time) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return tx.Set(ctx, c.indexKey, data)
}
