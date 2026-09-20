package componentconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/snakexgc/tdl/application"
	runtimeconfig "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

func object(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result map[string]any
	err = decoder.Decode(&result)
	return result, err
}

func put(root map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := root[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			root[part] = next
		}
		root = next
	}
	root[parts[len(parts)-1]] = value
}

// Load overlays all component-owned values, including defaults, onto bootstrap
// settings. It never reads sessions or accesses the network.
func Load(ctx context.Context, store *config.Store, bootstrap *runtimeconfig.Config) (*runtimeconfig.Config, map[string]bool, error) {
	if bootstrap == nil {
		bootstrap = runtimeconfig.DefaultConfig()
	}
	if store == nil {
		copy, err := runtimeconfig.Clone(bootstrap)
		return copy, nil, err
	}
	catalog, err := application.Catalog()
	if err != nil {
		return nil, nil, err
	}
	root, err := object(bootstrap)
	if err != nil {
		return nil, nil, err
	}
	views := map[string]config.View{}
	enabled := map[string]bool{}
	for _, definition := range catalog.Definitions() {
		id := definition.Manifest.ID
		document, err := store.Load(ctx, id)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", id, err)
		}
		view, err := catalog.View(ctx, id, document.Values)
		if err != nil {
			return nil, nil, err
		}
		views[id], enabled[id] = view, document.Enabled
	}
	for _, binding := range Bindings() {
		if binding.EnabledPath != "" {
			put(root, binding.EnabledPath, enabled[binding.Component])
		}
		for field, path := range binding.Fields {
			var raw json.RawMessage
			if err := views[binding.Component].Get(field, &raw); err != nil {
				return nil, nil, err
			}
			if path == "bot.allowed_users" {
				var users []string
				if err := json.Unmarshal(raw, &users); err != nil {
					return nil, nil, err
				}
				ids := make([]int64, 0, len(users))
				for _, user := range users {
					id, err := strconv.ParseInt(strings.TrimSpace(user), 10, 64)
					if err != nil {
						return nil, nil, err
					}
					ids = append(ids, id)
				}
				put(root, path, ids)
			} else {
				put(root, path, raw)
			}
		}
	}
	data, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	var result runtimeconfig.Config
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, nil, err
	}
	if err := runtimeconfig.Validate(&result); err != nil {
		return nil, nil, err
	}
	return &result, enabled, nil
}
