package application

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/rte/config"
)

// componentValues loads dynamic components through the same persistent store
// as static components. Disabled required resources fail before starting work.
func componentValues(ctx context.Context, id string, stores ...*config.Store) (map[string]map[string]any, error) {
	values := map[string]map[string]any{}
	if len(stores) == 0 || stores[0] == nil {
		return values, nil
	}
	document, err := stores[0].Load(ctx, id)
	if err != nil {
		return nil, err
	}
	if !document.Enabled {
		return nil, fmt.Errorf("required component %s is disabled", id)
	}
	values[id] = document.Values
	return values, nil
}
