package rte

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte/config"
)

// Directory joins declared components to resource-owning runtimes. Bindings
// resolve the current instance after a connection or process has restarted.
type Directory struct {
	mu        sync.RWMutex
	patchMu   sync.Mutex
	catalog   *Catalog
	store     *config.Store
	active    *config.Store
	hosts     map[string]func() *Runtime
	observers map[string]func() Health
}

var ErrConfigurationConflict = errors.New("configuration changed; reload before saving")

func NewDirectory(catalog *Catalog, store *config.Store) *Directory {
	return &Directory{catalog: catalog, store: store, hosts: map[string]func() *Runtime{}, observers: map[string]func() Health{}}
}

// WithActiveStore supplies the immutable configuration used by this process.
// Configure it before publishing the directory to concurrent callers.
func (d *Directory) WithActiveStore(active *config.Store) *Directory {
	d.active = active
	return d
}

func (d *Directory) Bind(name string, host func() *Runtime) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if name == "" || host == nil {
		return fmt.Errorf("host binding requires a name and resolver")
	}
	if _, exists := d.hosts[name]; exists {
		return fmt.Errorf("duplicate host binding %s", name)
	}
	d.hosts[name] = host
	return nil
}

func (d *Directory) Observe(name string, observe func() Health) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if name == "" || observe == nil {
		return fmt.Errorf("health binding requires a name and observer")
	}
	if _, exists := d.observers[name]; exists {
		return fmt.Errorf("duplicate health binding %s", name)
	}
	d.observers[name] = observe
	return nil
}

func (d *Directory) runtimes() []*Runtime {
	d.mu.RLock()
	hosts := make(map[string]func() *Runtime, len(d.hosts))
	for name, host := range d.hosts {
		hosts[name] = host
	}
	d.mu.RUnlock()
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[*Runtime]bool{}
	result := []*Runtime{}
	for _, name := range names {
		if host := hosts[name](); host != nil && !seen[host] {
			seen[host] = true
			result = append(result, host)
		}
	}
	return result
}

func (d *Directory) Health() []Health {
	d.mu.RLock()
	observers := make(map[string]func() Health, len(d.observers))
	for name, observe := range d.observers {
		observers[name] = observe
	}
	d.mu.RUnlock()
	result := []Health{}
	for _, host := range d.runtimes() {
		result = append(result, host.Health())
	}
	names := make([]string, 0, len(observers))
	for name := range observers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result = append(result, observers[name]())
	}
	return result
}

func (d *Directory) Configurations(ctx context.Context) []Configuration {
	d.patchMu.Lock()
	defer d.patchMu.Unlock()
	live := map[string]Configuration{}
	views := map[string]config.View{}
	for _, host := range d.runtimes() {
		host.mu.Lock()
		for _, entry := range host.configurationsLocked() {
			live[entry.ID] = entry
			views[entry.ID] = host.instances[entry.ID].config
		}
		host.mu.Unlock()
	}
	result := []Configuration{}
	for _, definition := range d.catalog.Definitions() {
		m := definition.Manifest
		entry, active := live[m.ID]
		if !active {
			entry = Configuration{ID: m.ID, Title: m.Title, State: Stopped, Fields: m.Config, Pages: m.Pages, Values: map[string]any{}}
		}
		entry.Feature = m.Feature
		entry.Scope, entry.Enabled = definition.Scope, true
		if d.store != nil {
			entry.Revision, _ = d.store.Revision(ctx, m.ID)
			document, err := d.store.Load(ctx, m.ID)
			entry.Enabled = document.Enabled
			if err != nil {
				entry.Error = err.Error()
			} else {
				view, err := d.catalog.View(ctx, m.ID, document.Values)
				if err != nil {
					entry.Error = err.Error()
				} else {
					entry.PendingRestart = active && entry.State == Running && !views[m.ID].Equal(view)
					entry.Values = publicValues(m.Config, view)
					entry.Previews = publicPreviews(m.Config, view)
					if d.active != nil {
						before, err := d.active.Load(ctx, m.ID)
						if err != nil {
							entry.Error = err.Error()
						} else if baseline, err := d.catalog.View(ctx, m.ID, before.Values); err != nil {
							entry.Error = err.Error()
						} else {
							entry.ActiveEnabled = &before.Enabled
							entry.Changes = configurationChanges(m.Config, baseline, view)
							if before.Enabled != document.Enabled {
								entry.Changes = append(entry.Changes, ConfigurationChange{Name: "enabled", Before: before.Enabled, After: document.Enabled})
							}
							entry.PendingRestart = len(entry.Changes) > 0
						}
					}
				}
			}
		} else if !active || entry.State == Stopped || entry.State == Failed || entry.State == Blocked {
			view, err := d.catalog.View(ctx, m.ID, nil)
			if err != nil {
				entry.Error = err.Error()
			} else {
				entry.Values = publicValues(m.Config, view)
				entry.Previews = publicPreviews(m.Config, view)
			}
		}
		for i := range entry.Fields {
			if entry.Fields[i].Secret {
				entry.Fields[i].Default = ""
			}
		}
		result = append(result, entry)
	}
	return result
}

// ResolveComponentPort checks ownership and active enablement on each request;
// callers never keep a handler from a replaced or disabled component instance.
func (d *Directory) ResolveComponentPort(id, name string) (any, error) {
	definition, ok := d.catalog.entries[id]
	if !ok {
		return nil, fmt.Errorf("unknown component %s", id)
	}
	declared := false
	for _, port := range definition.Manifest.Provides {
		declared = declared || port.Name == name
	}
	if !declared {
		return nil, fmt.Errorf("%s does not provide %s", id, name)
	}
	store := d.active
	if store == nil {
		store = d.store
	}
	if store != nil {
		document, err := store.Load(context.Background(), id)
		if err != nil {
			return nil, err
		}
		if !document.Enabled {
			return nil, fmt.Errorf("component %s is disabled", id)
		}
	}
	for _, host := range d.runtimes() {
		if value, err := host.resolveComponentPort(id, name); err == nil {
			return value, nil
		}
	}
	return nil, fmt.Errorf("component %s is unavailable", id)
}

// Patch validates and saves configuration without changing running components.
func (d *Directory) Patch(ctx context.Context, id string, patch map[string]any) error {
	return d.PatchWithRevision(ctx, id, patch, "")
}

func (d *Directory) PatchWithRevision(ctx context.Context, id string, patch map[string]any, expected string) error {
	d.patchMu.Lock()
	defer d.patchMu.Unlock()
	if d.store == nil {
		return fmt.Errorf("component configuration directory is not enabled")
	}
	if expected != "" {
		current, err := d.store.Revision(ctx, id)
		if err != nil {
			return err
		}
		if current != expected {
			return ErrConfigurationConflict
		}
	}
	definition, exists := d.catalog.entries[id]
	if !exists {
		return fmt.Errorf("unknown component %s", id)
	}
	document, err := d.store.Load(ctx, id)
	if err != nil {
		return err
	}
	values := document.Values
	if values == nil {
		values = map[string]any{}
	}
	for key, value := range patch {
		keep := false
		for _, field := range definition.Manifest.Config {
			if field.Name == key && (field.Secret || field.EmptyPreserves) {
				if text, ok := value.(string); ok && text == "" {
					keep = true
				}
				break
			}
		}
		if !keep {
			values[key] = value
		}
	}
	view, err := d.catalog.View(ctx, id, values)
	if err != nil {
		return err
	}
	return d.store.Save(ctx, id, document.Enabled, view)
}

func publicValues(fields []manifest.ConfigField, view config.View) map[string]any {
	values := map[string]any{}
	for _, field := range fields {
		if field.Secret {
			continue
		}
		var raw json.RawMessage
		if view.Get(field.Name, &raw) == nil {
			values[field.Name] = raw
		}
	}
	return values
}

// SetEnabled persists desired state for the next process start.
func (d *Directory) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return d.SetEnabledWithRevision(ctx, id, enabled, "")
}

func (d *Directory) SetEnabledWithRevision(ctx context.Context, id string, enabled bool, expected string) error {
	d.patchMu.Lock()
	defer d.patchMu.Unlock()
	if d.store == nil {
		return fmt.Errorf("component configuration directory is not enabled")
	}
	if expected != "" {
		current, err := d.store.Revision(ctx, id)
		if err != nil {
			return err
		}
		if current != expected {
			return ErrConfigurationConflict
		}
	}
	document, err := d.store.Load(ctx, id)
	if err != nil {
		return err
	}
	view, err := d.catalog.View(ctx, id, document.Values)
	if err != nil {
		return err
	}
	return d.store.Save(ctx, id, enabled, view)
}
