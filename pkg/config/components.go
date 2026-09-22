package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/config"
)

// System selects the process-owned settings from a transport snapshot.
func System(cfg *Config) ports.SystemConfiguration {
	return ports.SystemConfiguration{Namespace: cfg.Namespace, Debug: cfg.Debug}
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

// Load reads every component from the unified repository. Only system settings
// come from the process snapshot; business values cannot fall back to it.
func Load(ctx context.Context, store *config.Store, system ports.SystemConfiguration) (*Config, map[string]bool, error) {
	if store == nil {
		return nil, nil, fmt.Errorf("component configuration store is required")
	}
	return loadComponents(ctx, store, system)
}

func loadComponents(ctx context.Context, store *config.Store, system ports.SystemConfiguration) (*Config, map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	catalog, err := application.Catalog()
	if err != nil {
		return nil, nil, err
	}
	root := map[string]any{"namespace": system.Namespace, "debug": system.Debug}
	views := map[string]config.View{}
	enabled := map[string]bool{}
	for _, definition := range catalog.Definitions() {
		id := definition.Manifest.ID
		document := config.Document{Enabled: id != forwardTriggerComponentID}
		if store != nil {
			document, err = store.Load(ctx, id)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", id, err)
			}
		}
		view, err := catalog.View(ctx, id, document.Values)
		if err != nil {
			return nil, nil, err
		}
		views[id], enabled[id] = view, document.Enabled
	}
	for _, binding := range bindings() {
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
	var result Config
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, nil, err
	}
	// HTTP serves every download mode, including links copied to external tools.
	result.Modules.HTTP = true
	enabled[proxyComponentID] = true
	return &result, enabled, nil
}
