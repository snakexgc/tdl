package rte

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
)

// BuildStored loads component views from the unified configuration repository.
// Disabled components do not instantiate factories.
func (r *Registry) BuildStored(ctx context.Context, account types.AccountID, store *config.Store) (*Runtime, error) {
	values := make(map[string]map[string]any, len(r.entries))
	enabled := make(map[string]bool, len(r.entries))
	for id := range r.entries {
		document, err := store.Load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		values[id], enabled[id] = document.Values, document.Enabled
	}
	return r.Build(account, enabled, values)
}
