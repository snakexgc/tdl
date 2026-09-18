package taskhub

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/snakexgc/tdl/internal/core/storage"
)

const (
	ForwardPrefix = "forward.job."
	ForwardIndex  = "forward.index"
	LinkPrefix    = "watch.download."
	LinkIndex     = "watch.download.index"
	Aria2Prefix   = "watch.aria2.task."
	Aria2Index    = "watch.aria2.index"
	LocalPrefix   = "watch.internal.task."
	LocalIndex    = "watch.internal.index"
)

func owned(store storage.Storage, selectCollection func(*Hub) *Collection) *Collection {
	hub, err := Open(store)
	if err != nil {
		return &Collection{initErr: err}
	}
	return selectCollection(hub)
}

func Links(s storage.Storage) *Collection {
	return owned(s, func(h *Hub) *Collection { return h.Links })
}

func Aria2(s storage.Storage) *Collection {
	return owned(s, func(h *Hub) *Collection { return h.Aria2 })
}

func Local(s storage.Storage) *Collection {
	return owned(s, func(h *Hub) *Collection { return h.Local })
}

func Forward(s storage.Storage) *Collection {
	return owned(s, func(h *Hub) *Collection { return h.Forward })
}

// Mutation changes a single existing record. Returning nil deletes it; returning
// unchanged bytes preserves it. The callback is serialized with every collection
// operation, including expiry checks, and must not reenter the repository.
func (c *Collection) Mutate(ctx context.Context, id string, fn func([]byte, time.Time) ([]byte, time.Time, error)) error {
	if c.initErr != nil {
		return c.initErr
	}
	return storage.Update(ctx, c.store, func(tx storage.Storage) error {
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		data, err := tx.Get(ctx, c.prefix+id)
		if err != nil {
			return err
		}
		next, stamp, err := fn(data, index[id])
		if err != nil {
			return err
		}
		if next == nil {
			if err := tx.Delete(ctx, c.prefix+id); err != nil {
				return err
			}
			delete(index, id)
		} else {
			if !json.Valid(next) {
				return errors.New("invalid task JSON")
			}
			if string(next) != string(data) {
				if err := tx.Set(ctx, c.prefix+id, next); err != nil {
					return err
				}
			}
			if previous, ok := index[id]; ok && previous.Equal(stamp) {
				return nil
			}
			index[id] = stamp
		}
		return c.saveIndex(ctx, tx, index)
	})
}

// Merge updates metadata without removing status fields owned by other reports.
func (c *Collection) Merge(ctx context.Context, id string, data []byte, stamp time.Time) error {
	if c.initErr != nil {
		return c.initErr
	}
	if id == "" {
		return errors.New("empty task id")
	}
	return storage.Update(ctx, c.store, func(tx storage.Storage) error {
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		old, err := tx.Get(ctx, c.prefix+id)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		merged := make(map[string]json.RawMessage)
		if err == nil {
			if err := json.Unmarshal(old, &merged); err != nil {
				return err
			}
		}
		if merged == nil {
			merged = make(map[string]json.RawMessage)
		}
		var updates map[string]json.RawMessage
		if err := json.Unmarshal(data, &updates); err != nil {
			return err
		}
		if c.prefix == Aria2Prefix {
			if err := mergeAria2State(merged, updates); err != nil {
				return err
			}
		}
		// A metadata refresh may have been prepared before a concurrent download
		// touched the link. Never overwrite that newer activity clock.
		if c.prefix == LinkPrefix {
			var oldClock, newClock time.Time
			_ = json.Unmarshal(merged["last_active_at"], &oldClock)
			_ = json.Unmarshal(updates["last_active_at"], &newClock)
			if oldClock.After(newClock) {
				delete(updates, "last_active_at")
			}
		}
		for field, value := range updates {
			merged[field] = value
		}
		data, err = json.Marshal(merged)
		if err != nil {
			return err
		}
		if err := tx.Set(ctx, c.prefix+id, data); err != nil {
			return err
		}
		// Metadata refresh must not move the persisted activity index backwards.
		if previous, exists := index[id]; !exists || stamp.After(previous) {
			index[id] = stamp
		}
		return c.saveIndex(ctx, tx, index)
	})
}

// Sweep checks the current record and index under the same transaction. A
// snapshot taken by a caller is never sufficient authority to delete a task.
func (c *Collection) Sweep(ctx context.Context, expired func([]byte, time.Time) (bool, error)) error {
	if c.initErr != nil {
		return c.initErr
	}
	return storage.Update(ctx, c.store, func(tx storage.Storage) error {
		index, err := c.index(ctx, tx)
		if err != nil {
			return err
		}
		changed := false
		for id, stamp := range index {
			data, err := tx.Get(ctx, c.prefix+id)
			missing := errors.Is(err, storage.ErrNotFound)
			if err != nil && !missing {
				return err
			}
			remove := missing
			if !missing {
				remove, err = expired(data, stamp)
				if err != nil {
					return err
				}
			}
			if remove {
				if err := tx.Delete(ctx, c.prefix+id); err != nil {
					return err
				}
				delete(index, id)
				changed = true
			}
		}
		if changed {
			return c.saveIndex(ctx, tx, index)
		}
		return nil
	})
}

func (c *Collection) MarkDownloaded(ctx context.Context, id string) error {
	return c.Mutate(ctx, id, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, stamp, err
		}
		if raw == nil {
			raw = make(map[string]json.RawMessage)
		}
		raw["downloaded"] = json.RawMessage("true")
		if value, ok := raw["id"]; !ok || string(value) == `""` {
			raw["id"], _ = json.Marshal(id)
		}
		next, err := json.Marshal(raw)
		return next, stamp, err
	})
}
