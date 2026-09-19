package componentconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/snakexgc/tdl/application"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
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

func get(root map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := root[part].(map[string]any)
		if !ok {
			return nil, false
		}
		root = next
	}
	value, ok := root[parts[len(parts)-1]]
	return value, ok
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

// Export preserves old defaults and enabled flags while exporting every schema.
func Export(cfg *legacy.Config, catalog *rte.Catalog) (map[string]config.Document, error) {
	root, err := object(cfg)
	if err != nil {
		return nil, err
	}
	put(root, proxyField, legacy.EffectiveProxy(cfg))
	botProxy := cfg.Bot.Proxy
	if botProxy == "" {
		botProxy = legacy.EffectiveProxy(cfg)
	}
	put(root, "bot.proxy", botProxy)
	// Import the historical shared directory once. Local downloads never read
	// or create paths belonging to the remote aria2 host after migration.
	if cfg.Downloader.LocalRoot == "" && legacy.EffectiveDownloaderMode(cfg) == legacy.DownloaderModeInternal {
		if dir := strings.TrimSpace(cfg.Aria2.Dir); dir != "" {
			absolute, err := filepath.Abs(dir)
			if err != nil {
				return nil, fmt.Errorf("resolve legacy local download directory: %w", err)
			}
			put(root, "downloader.local_root", absolute)
		}
	}
	documents := map[string]config.Document{}
	for _, definition := range catalog.Definitions() {
		documents[definition.Manifest.ID] = config.Document{Version: config.CurrentVersion, Enabled: true, Values: map[string]any{}}
	}
	for _, binding := range Bindings() {
		document := documents[binding.Component]
		if binding.EnabledPath != "" {
			value, _ := get(root, binding.EnabledPath)
			document.Enabled, _ = value.(bool)
		}
		for field, path := range binding.Fields {
			if value, ok := get(root, path); ok {
				document.Values[field] = value
			}
		}
		documents[binding.Component] = document
	}
	users := make([]string, 0, len(cfg.Bot.AllowedUsers))
	for _, id := range cfg.Bot.AllowedUsers {
		users = append(users, strconv.FormatInt(id, 10))
	}
	documents["console.bot"].Values["allowed_users"] = users
	documents["notify.telegram"].Values["recipients"] = users
	documents["update.self"].Values[proxyField] = legacy.EffectiveProxy(cfg)
	return documents, nil
}

// Load overlays all component-owned values, including defaults, onto bootstrap
// settings. It never writes the compatibility file or reads sessions/network.
func Load(ctx context.Context, store *config.Store, bootstrap *legacy.Config) (*legacy.Config, map[string]bool, error) {
	if bootstrap == nil {
		bootstrap = legacy.DefaultConfig()
	}
	if store == nil {
		copy, err := legacy.Clone(bootstrap)
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
	put(root, "proxy_username", "")
	put(root, "proxy_password", "")
	put(root, "http.listen", "")
	put(root, "webui.listen", "")
	data, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	var result legacy.Config
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, nil, err
	}
	if err := legacy.Validate(&result); err != nil {
		return nil, nil, err
	}
	return &result, enabled, nil
}
