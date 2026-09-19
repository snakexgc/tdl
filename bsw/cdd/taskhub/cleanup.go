package taskhub

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/snakexgc/tdl/internal/core/storage"
)

// CleanupLinksAndAria2 checks live data, rather than a UI snapshot, before
// removing expired links and orphaned aria2 associations.
func CleanupLinksAndAria2(ctx context.Context, s storage.Storage, now time.Time, ttl time.Duration) error {
	return storage.Update(ctx, s, func(tx storage.Storage) error {
		links, aria := Links(tx), Aria2(tx)
		linkIndex, err := links.index(ctx, tx)
		if err != nil {
			return err
		}
		ariaIndex, err := aria.index(ctx, tx)
		if err != nil {
			return err
		}
		for id, stamp := range linkIndex {
			data, err := tx.Get(ctx, LinkPrefix+id)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			missing := errors.Is(err, storage.ErrNotFound)
			if !missing {
				var record struct {
					CreatedAt    time.Time `json:"created_at"`
					LastActiveAt time.Time `json:"last_active_at"`
				}
				if err := json.Unmarshal(data, &record); err != nil {
					return err
				}
				if !record.LastActiveAt.IsZero() {
					stamp = record.LastActiveAt
				} else if !record.CreatedAt.IsZero() {
					stamp = record.CreatedAt
				}
			}
			if missing || expired(stamp, now, ttl) {
				if err := tx.Delete(ctx, LinkPrefix+id); err != nil {
					return err
				}
				delete(linkIndex, id)
			}
		}
		for id, stamp := range ariaIndex {
			data, err := tx.Get(ctx, Aria2Prefix+id)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			remove := errors.Is(err, storage.ErrNotFound) || expired(stamp, now, ttl)
			if !errors.Is(err, storage.ErrNotFound) {
				var record struct {
					TaskID       string    `json:"task_id"`
					Deleted      bool      `json:"deleted"`
					ControlUntil time.Time `json:"control_until"`
				}
				if err := json.Unmarshal(data, &record); err != nil {
					return err
				}
				if record.ControlUntil.After(now) {
					continue
				}
				if !remove && record.TaskID != "" && !record.Deleted {
					_, err := tx.Get(ctx, LinkPrefix+record.TaskID)
					if err != nil && !errors.Is(err, storage.ErrNotFound) {
						return err
					}
					remove = errors.Is(err, storage.ErrNotFound)
				}
			}
			if remove {
				if err := tx.Delete(ctx, Aria2Prefix+id); err != nil {
					return err
				}
				delete(ariaIndex, id)
			}
		}
		if err := links.saveIndex(ctx, tx, linkIndex); err != nil {
			return err
		}
		return aria.saveIndex(ctx, tx, ariaIndex)
	})
}

func expired(stamp, now time.Time, ttl time.Duration) bool {
	return ttl > 0 && !stamp.IsZero() && !stamp.Add(ttl).After(now)
}
