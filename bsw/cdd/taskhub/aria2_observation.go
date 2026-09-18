package taskhub

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type Aria2Observations struct{ Links LinkRepository }

func linkVersion(data []byte) string {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) == nil {
		for _, field := range []string{"last_active_at", "downloaded", "http_delivery"} {
			delete(raw, field)
		}
		if canonical, err := json.Marshal(raw); err == nil {
			data = canonical
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (r Aria2Observations) Snapshot(ctx context.Context) (ports.Aria2ObservationSnapshot, error) {
	snapshot := ports.Aria2ObservationSnapshot{Links: map[string]ports.ObservedLink{}, Records: map[string]types.Aria2TaskRecord{}}
	pairs, err := r.Links.Snapshot(ctx)
	if err != nil {
		return snapshot, err
	}
	for key, data := range pairs {
		switch {
		case strings.HasPrefix(key, LinkPrefix) && key != LinkIndex:
			id := strings.TrimPrefix(key, LinkPrefix)
			var task types.PersistentLink
			if err := json.Unmarshal(data, &task); err != nil {
				return snapshot, err
			}
			if task.ID != "" && task.ID != id {
				continue
			}
			task.ID = id
			snapshot.Links[id] = ports.ObservedLink{Task: task, Version: linkVersion(data)}
		case strings.HasPrefix(key, Aria2Prefix) && key != Aria2Index:
			gid := strings.TrimPrefix(key, Aria2Prefix)
			var record types.Aria2TaskRecord
			if err := json.Unmarshal(data, &record); err != nil {
				return snapshot, err
			}
			if record.GID != "" && record.GID != gid {
				continue
			}
			record.GID = gid
			snapshot.Records[gid] = record
		}
	}
	return snapshot, nil
}

// Apply atomically validates source identity, remote revision and terminal state
// before changing an association or its link. A stale snapshot cannot recreate
// a deleted record, or mark a replacement link as downloaded.
func (r Aria2Observations) Apply(ctx context.Context, source ports.ObservedLink, observed types.Aria2TaskRecord, create bool, now time.Time, ttl time.Duration) (bool, error) {
	if observed.GID == "" || observed.GID == "index" || strings.ContainsAny(observed.GID, `/\\`) {
		return false, errors.New("invalid aria2 gid")
	}
	applied := false
	err := storage.Update(ctx, r.Links.Store, func(tx storage.Storage) error {
		var linkData []byte
		if source.Task.ID != "" {
			if source.Task.ID != observed.TaskID {
				return errors.New("link association mismatch")
			}
			var err error
			linkData, err = tx.Get(ctx, LinkPrefix+source.Task.ID)
			if errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if linkVersion(linkData) != source.Version {
				return nil
			}
		} else if create {
			return nil
		}
		data, err := tx.Get(ctx, Aria2Prefix+observed.GID)
		missing := errors.Is(err, storage.ErrNotFound)
		if err != nil && !missing {
			return err
		}
		if create != missing {
			return nil
		}
		raw := map[string]json.RawMessage{}
		if !create {
			var current types.Aria2TaskRecord
			if err := json.Unmarshal(data, &current); err != nil {
				return err
			}
			if current.Revision != observed.Revision || current.TaskID != observed.TaskID || !types.DownloadTransition(types.NormalizeDownloadState(current.Status), types.NormalizeDownloadState(observed.Status)) {
				return nil
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				return err
			}
		} else {
			if observed.CreatedAt.IsZero() {
				observed.CreatedAt = now
			}
			data, err = json.Marshal(observed)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				return err
			}
		}
		for field, value := range map[string]any{fieldStatus: observed.Status, fieldState: types.NormalizeDownloadState(observed.Status), fieldTotal: observed.Total, fieldCompleted: observed.Completed, fieldError: observed.Error, fieldRevision: observed.Revision + 1} {
			raw[field], _ = json.Marshal(value)
		}
		data, err = json.Marshal(raw)
		if err != nil {
			return err
		}
		if err := tx.Set(ctx, Aria2Prefix+observed.GID, data); err != nil {
			return err
		}
		aria := Aria2(tx)
		index, err := aria.index(ctx, tx)
		if err != nil {
			return err
		}
		complete := observed.Status == "complete" && (observed.Total == 0 || observed.Completed >= observed.Total)
		stamp := observed.CreatedAt
		if !complete {
			stamp = now
		}
		if index[observed.GID].After(stamp) {
			stamp = index[observed.GID]
		}
		index[observed.GID] = stamp
		if err := aria.saveIndex(ctx, tx, index); err != nil {
			return err
		}
		if linkData != nil {
			var link map[string]json.RawMessage
			if err := json.Unmarshal(linkData, &link); err != nil {
				return err
			}
			if complete {
				link["downloaded"] = json.RawMessage("true")
			} else if ttl > 0 {
				var last time.Time
				_ = json.Unmarshal(link["last_active_at"], &last)
				if last.Before(now) {
					link["last_active_at"], _ = json.Marshal(now)
				}
			}
			linkData, err = json.Marshal(link)
			if err != nil {
				return err
			}
			if err := tx.Set(ctx, LinkPrefix+source.Task.ID, linkData); err != nil {
				return err
			}
			links := Links(tx)
			linkIndex, err := links.index(ctx, tx)
			if err != nil {
				return err
			}
			linkStamp := source.Task.LastActiveAt
			if linkStamp.IsZero() {
				linkStamp = source.Task.CreatedAt
			}
			if !complete && ttl > 0 {
				linkStamp = now
			}
			if linkIndex[source.Task.ID].After(linkStamp) {
				linkStamp = linkIndex[source.Task.ID]
			}
			linkIndex[source.Task.ID] = linkStamp
			if err := links.saveIndex(ctx, tx, linkIndex); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	return applied && err == nil, err
}

func (r Aria2Observations) Cleanup(ctx context.Context, now time.Time, ttl time.Duration) error {
	return CleanupLinksAndAria2(ctx, r.Links.Store, now, ttl)
}
