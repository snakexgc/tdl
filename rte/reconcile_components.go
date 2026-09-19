package rte

import (
	"context"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/rte/config"
)

// ReconcileComponents changes enablement inside a host without replacing its
// unrelated components. Desired is a validated, unstarted registry build.
// Resource-sensitive configuration continues through its resource owner.
func (r *Runtime) ReconcileComponents(ctx context.Context, desired *Runtime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.stopped || desired == nil || desired.started || desired.account != r.account {
		return fmt.Errorf("invalid component reconciliation lifecycle")
	}
	affected := map[string]bool{}
	for id := range r.instances {
		if desired.instances[id] == nil || r.instances[id].status.State != Running {
			affected[id] = true
		}
	}
	drainCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A required or optional port snapshot must be rebound if its provider
	// disappears or changes. This closure contains only actual dependants.
	for changed := true; changed; {
		changed = false
		for id, item := range r.instances {
			if affected[id] {
				continue
			}
			for _, req := range item.registration.Manifest.Requires {
				old, next := r.providers[req.Name], desired.providers[req.Name]
				if old != next || affected[old] {
					affected[id], changed = true, true
					break
				}
			}
		}
	}
	for id := range affected {
		item := r.instances[id]
		if item.cancel != nil {
			item.cancel()
		}
		item.setStatus(Status{ID: id, State: Stopping})
	}
	for i := len(r.order) - 1; i >= 0; i-- {
		id := r.order[i]
		if !affected[id] {
			continue
		}
		item := r.instances[id]
		if err := r.bus.WaitScope(drainCtx, item.owner); err != nil {
			return err
		}
		if item.runnables != nil {
			if err := item.runnables.Stop(drainCtx); err != nil {
				return err
			}
		}
		if item.initialized {
			if err := invoke(func() error { return item.component.Stop(drainCtx) }); err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
			item.initialized = false
		}
		for _, provided := range item.registration.Manifest.Provides {
			delete(r.ports, provided.Name)
		}
		item.setStatus(Status{ID: id, State: Stopped})
	}
	for id, item := range desired.instances {
		if current := r.instances[id]; current != nil && !affected[id] {
			desired.instances[id] = current
		} else {
			r.instances[id] = item
		}
	}
	r.instances, r.providers, r.order = desired.instances, desired.providers, desired.order
	r.registry = desired.registry
	r.startLocked(ctx)
	return nil
}

// ReconcileSaved changes enablement inside a resource owner's original
// registry, including components disabled when that owner was first created.
func (r *Runtime) ReconcileSaved(ctx context.Context, store *config.Store) error {
	if store == nil {
		return nil
	}
	r.mu.Lock()
	registry, account, stopped := r.registry, r.account, r.stopped || r.stopping
	r.mu.Unlock()
	if stopped {
		return nil
	}
	values, enabled := map[string]map[string]any{}, map[string]bool{}
	for id := range registry.entries {
		document, err := store.Load(ctx, id)
		if err != nil {
			return err
		}
		values[id], enabled[id] = document.Values, document.Enabled
	}
	desired, err := registry.Build(account, enabled, values)
	if err != nil {
		return err
	}
	return r.ReconcileComponents(ctx, desired)
}
