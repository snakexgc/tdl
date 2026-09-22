package rte

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/snakexgc/tdl/rte/config"
)

// PreparedConfig validates and builds a new immutable snapshot without changing
// live state. The returned commit must be infallible and must not call Runtime.
type PreparedConfig interface {
	PrepareConfig(context.Context, config.View) (commit func(), err error)
}

// ReconfigureBatch prepares every changed component before publishing any of
// them. Invalid input cannot leave half of a policy update applied. Commits are
// individually atomic; this does not promise a cross-port reader transaction.
func (r *Runtime) ReconfigureBatch(ctx context.Context, values map[string]map[string]any) error {
	return r.reconfigureBatch(ctx, values, nil)
}

// ReconfigureSaved validates and prepares a component, persists its complete
// configuration, then publishes it. A failed save cannot change live policy.
func (r *Runtime) ReconfigureSaved(ctx context.Context, id string, values map[string]any, store *config.Store) error {
	return r.reconfigureBatch(ctx, map[string]map[string]any{id: values}, func(views map[string]config.View) error {
		return store.Save(ctx, id, true, views[id])
	})
}

func (r *Runtime) reconfigureBatch(ctx context.Context, values map[string]map[string]any, persist func(map[string]config.View) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconfigureLocked(ctx, values, persist)
}

func (r *Runtime) reconfigureLocked(ctx context.Context, values map[string]map[string]any, persist func(map[string]config.View) error) error {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type change struct {
		item   *instance
		view   config.View
		commit func()
	}
	changes := make([]change, 0, len(ids))
	views := make(map[string]config.View, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		item, ok := r.instances[id]
		if !ok {
			return fmt.Errorf("unknown component %s", id)
		}
		view, err := config.New(item.registration.Manifest.Config, values[id])
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		views[id] = view
		if item.status.State != Running {
			return fmt.Errorf("component %s is not running", id)
		}
		if item.config.Equal(view) {
			continue
		}
		prepared, ok := item.component.(PreparedConfig)
		if !ok {
			return fmt.Errorf("component %s does not support prepared configuration", id)
		}
		var commit func()
		if err := invoke(func() error { var err error; commit, err = prepared.PrepareConfig(ctx, view); return err }); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if commit == nil {
			return fmt.Errorf("%s: prepare returned no commit", id)
		}
		changes = append(changes, change{item, view, commit})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if persist != nil {
		if err := persist(views); err != nil {
			return err
		}
	}
	for _, next := range changes {
		next.commit()
		next.item.config = next.view
		next.item.setStatus(next.item.status)
		slog.Info("组件配置已生效", "component", next.item.status.ID, "account", r.account)
	}
	return nil
}

// PatchSaved merges fields under the same runtime lock as prepare and commit.
// An omitted or empty sensitive field retains its current value.
func (r *Runtime) PatchSaved(ctx context.Context, id string, patch map[string]any, store *config.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.instances[id]
	if !ok {
		return fmt.Errorf("unknown component %s", id)
	}
	values := make(map[string]any)
	for _, field := range item.registration.Manifest.Config {
		var raw json.RawMessage
		if err := item.config.Get(field.Name, &raw); err != nil {
			return err
		}
		values[field.Name] = raw
	}
	for key, value := range patch {
		keep := false
		for _, field := range item.registration.Manifest.Config {
			if field.Name == key && field.Secret {
				if text, ok := value.(string); ok && text == "" {
					keep = true
				}
			}
		}
		if !keep {
			values[key] = value
		}
	}
	return r.reconfigureLocked(ctx, map[string]map[string]any{id: values}, func(views map[string]config.View) error { return store.Save(ctx, id, true, views[id]) })
}
