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

// Snapshot includes legacy unindexed records, but never exposes account
// credentials or unrelated datasets to catalog consumers.
func (r LinkRepository) Snapshot(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Store == nil && r.Engine == nil {
		return nil, errors.New("namespace storage is not configured")
	}
	if r.Engine == nil {
		result := map[string][]byte{}
		for _, collection := range []*Collection{Links(r.Store), Aria2(r.Store)} {
			records, err := collection.Records(ctx)
			if err != nil {
				return nil, err
			}
			for id, data := range records {
				result[collection.prefix+id] = append([]byte(nil), data...)
			}
		}
		return result, ctx.Err()
	}
	meta, err := r.Engine.MigrateTo()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte)
	for key, data := range meta[r.Namespace] {
		if (strings.HasPrefix(key, LinkPrefix) && key != LinkIndex) || (strings.HasPrefix(key, Aria2Prefix) && key != Aria2Index) {
			result[key] = append([]byte(nil), data...)
		}
	}
	return result, ctx.Err()
}

// Remove atomically removes source metadata and its remote associations.
// Active local execution must be stopped through the download control port first.
func (r LinkRepository) Remove(ctx context.Context, id string) (int, error) {
	if r.Store == nil && r.Engine == nil {
		return 0, errors.New("namespace storage is not configured")
	}
	if id == "" || id == "index" || strings.ContainsAny(id, "/\\") {
		return 0, errors.New("invalid download link id")
	}
	removed := 0
	// Legacy records may predate the index. Discover candidates outside the
	// transaction, then re-read their association under the write lock.
	candidates := make(map[string]struct{})
	if r.Engine != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		snapshot, err := r.Engine.MigrateTo()
		if err != nil {
			return 0, err
		}
		for key := range snapshot[r.Namespace] {
			if strings.HasPrefix(key, Aria2Prefix) && key != Aria2Index {
				candidates[strings.TrimPrefix(key, Aria2Prefix)] = struct{}{}
			}
		}
	}
	err := storage.Update(ctx, r.Store, func(tx storage.Storage) error {
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
			candidates[gid] = struct{}{}
		}
		for gid := range candidates {
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
