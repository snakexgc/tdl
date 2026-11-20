// Package listen owns the account-scoped download listener journal.
package listen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/services/nvm"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const prefix = "download.listen."

type Repository struct {
	store storage.Storage
	err   error
}

func New(store storage.Storage) *Repository {
	r := &Repository{}
	if store == nil {
		r.err = errors.New("监听持久化存储不可用")
		return r
	}
	registry := nvm.New(store)
	r.err = registry.Register(nvm.Dataset{Name: "download.listen", Writer: "download.listen", Prefix: prefix})
	if r.err == nil {
		r.store, r.err = registry.Writer("download.listen", "download.listen")
	}
	return r
}

type metadata struct {
	Version       int                                        `json:"version"`
	Policy        types.ListenPolicyRecord                   `json:"policy"`
	PendingPolicy types.ListenPolicyRecord                   `json:"pending_policy"`
	Sources       map[types.ChatRef]types.ListenSourceRecord `json:"sources"`
}

func readMetadata(ctx context.Context, store storage.Storage) (metadata, error) {
	m := metadata{Version: types.ListenRecordVersion, Sources: map[types.ChatRef]types.ListenSourceRecord{}}
	raw, err := store.Get(ctx, prefix+"state")
	if errors.Is(err, storage.ErrNotFound) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	if m.Version != types.ListenRecordVersion || m.Sources == nil {
		return m, errors.New("不支持或损坏的监听状态版本")
	}
	for ref, source := range m.Sources {
		if source.Version != types.ListenRecordVersion || source.Ref != ref || source.ActivatedAt <= 0 {
			return m, errors.New("无效的监听来源记录")
		}
	}
	return m, nil
}

func (r *Repository) Load(ctx context.Context) (types.ListenState, error) {
	s := types.ListenState{Sources: map[types.ChatRef]types.ListenSourceRecord{}, Threads: map[string]types.ListenThreadRecord{}, Jobs: map[string]types.ListenJobRecord{}}
	if r.err != nil {
		return s, r.err
	}
	m, err := readMetadata(ctx, r.store)
	if err != nil {
		return s, err
	}
	s.Policy, s.PendingPolicy, s.Sources = m.Policy, m.PendingPolicy, m.Sources
	threads, err := taskhub.New(r.store, prefix+"thread.", prefix+"threads").Records(ctx)
	if err != nil {
		return s, err
	}
	for id, raw := range threads {
		var record types.ListenThreadRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return s, err
		}
		if record.Version != types.ListenRecordVersion || record.ID != id || record.PostID <= 0 || record.PostDate <= 0 || record.ExpiresAt.IsZero() {
			return s, errors.New("无效的评论线程记录")
		}
		s.Threads[id] = record
	}
	jobs, err := taskhub.New(r.store, prefix+"job.", prefix+"jobs").Records(ctx)
	if err != nil {
		return s, err
	}
	for id, raw := range jobs {
		var record types.ListenJobRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return s, err
		}
		if record.Version != types.ListenRecordVersion || record.ID != id || record.Message.ID <= 0 || !validState(record.State) {
			return s, errors.New("无效的监听事件记录")
		}
		s.Jobs[id] = record
	}
	return s, nil
}

func validState(state string) bool {
	switch state {
	case "pending", "submitting", "accepted", "filtered", "uncertain", "obsolete", "failed":
		return true
	default:
		return false
	}
}

func (r *Repository) Update(ctx context.Context, change types.ListenMutation) error {
	if r.err != nil {
		return r.err
	}
	return storage.Update(ctx, r.store, func(tx storage.Storage) error {
		m, err := readMetadata(ctx, tx)
		if err != nil {
			return err
		}
		if change.Policy != nil {
			m.Policy = *change.Policy
		}
		if change.PendingPolicy != nil {
			m.PendingPolicy = *change.PendingPolicy
		}
		for _, source := range change.Sources {
			m.Sources[source.Ref] = source
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := tx.Set(ctx, prefix+"state", raw); err != nil {
			return err
		}
		if len(change.Threads) > 0 || len(change.DeleteThreads) > 0 {
			threads := map[string]time.Time{}
			if err := readIndex(ctx, tx, prefix+"threads", &threads); err != nil {
				return err
			}
			for _, thread := range change.Threads {
				data, err := json.Marshal(thread)
				if err != nil {
					return err
				}
				if err := tx.Set(ctx, prefix+"thread."+thread.ID, data); err != nil {
					return fmt.Errorf("persist thread: %w", err)
				}
				threads[thread.ID] = time.Unix(thread.PostDate, 0)
			}
			for _, id := range change.DeleteThreads {
				if err := tx.Delete(ctx, prefix+"thread."+id); err != nil {
					return err
				}
				delete(threads, id)
			}
			if err := writeJSON(ctx, tx, prefix+"threads", threads); err != nil {
				return err
			}
		}
		if len(change.Jobs) == 0 {
			return nil
		}
		jobs := map[string]time.Time{}
		if err := readIndex(ctx, tx, prefix+"jobs", &jobs); err != nil {
			return err
		}
		for _, job := range change.Jobs {
			data, err := json.Marshal(job)
			if err != nil {
				return err
			}
			if err := tx.Set(ctx, prefix+"job."+job.ID, data); err != nil {
				return fmt.Errorf("persist event: %w", err)
			}
			jobs[job.ID] = time.Unix(job.Message.Date, 0)
		}
		return writeJSON(ctx, tx, prefix+"jobs", jobs)
	})
}

func readIndex(ctx context.Context, tx storage.Storage, key string, target *map[string]time.Time) error {
	raw, err := tx.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	if *target == nil {
		return errors.New("invalid listener index")
	}
	return nil
}

func writeJSON(ctx context.Context, tx storage.Storage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Set(ctx, key, raw)
}
