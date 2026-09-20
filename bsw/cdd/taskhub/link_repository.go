package taskhub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

type LinkRepository struct {
	Store     storage.Storage
	Engine    kv.Storage
	Namespace string
}

func (r LinkRepository) store() (storage.Storage, error) {
	if r.Store != nil {
		return r.Store, nil
	}
	if r.Engine != nil {
		return r.Engine.Open(r.Namespace)
	}
	return nil, errors.New("namespace storage is not configured")
}

// Snapshot returns indexed links and associations from one account transaction.
func (r LinkRepository) Snapshot(ctx context.Context) (map[string][]byte, error) {
	store, err := r.store()
	if err != nil {
		return nil, err
	}
	result := map[string][]byte{}
	err = storage.Update(ctx, store, func(tx storage.Storage) error {
		for _, collection := range []*Collection{Links(tx), Aria2(tx)} {
			index, err := collection.index(ctx, tx)
			if err != nil {
				return err
			}
			for id := range index {
				data, err := tx.Get(ctx, collection.prefix+id)
				if errors.Is(err, storage.ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				result[collection.prefix+id] = data
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Remove atomically removes source metadata and its remote associations.
// Active local execution must be stopped through the download control port first.
func (r LinkRepository) Remove(ctx context.Context, id string) (int, error) {
	store, err := r.store()
	if err != nil {
		return 0, err
	}
	if id == "" || id == "index" || strings.ContainsAny(id, "/\\") {
		return 0, errors.New("invalid download link id")
	}
	removed := 0

	err = storage.Update(ctx, store, func(tx storage.Storage) error {
		links, aria := Links(tx), Aria2(tx)
		linkIndex, err := links.index(ctx, tx)
		if err != nil {
			return err
		}
		ariaIndex, err := aria.index(ctx, tx)
		if err != nil {
			return err
		}
		for gid := range ariaIndex {
			data, err := tx.Get(ctx, Aria2Prefix+gid)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			var record struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			if record.TaskID != id {
				continue
			}
			if err := tx.Delete(ctx, Aria2Prefix+gid); err != nil {
				return err
			}
			delete(ariaIndex, gid)
			removed++
		}
		if _, err := tx.Get(ctx, LinkPrefix+id); err == nil {
			if err := tx.Delete(ctx, LinkPrefix+id); err != nil {
				return err
			}
			removed++
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		delete(linkIndex, id)
		if err := links.saveIndex(ctx, tx, linkIndex); err != nil {
			return err
		}
		return aria.saveIndex(ctx, tx, ariaIndex)
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}
