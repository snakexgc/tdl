package rte

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/rte/config"
)

// ExportConfig writes the active composition to a caller-owned staging store.
// Each document is atomic; callers must publish the directory only after success.
func (r *Runtime) ExportConfig(ctx context.Context, store *config.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.order {
		if r.instances[id].status.State != Running {
			return fmt.Errorf("component %s is not running", id)
		}
	}
	for _, id := range r.order {
		if err := store.Save(ctx, id, true, r.instances[id].config); err != nil {
			return fmt.Errorf("save %s: %w", id, err)
		}
	}
	return ctx.Err()
}
