package rte

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
)

// Scope describes the resource lifetime, not a separate configuration namespace.
type Scope string

const (
	ProcessScope    Scope = "process"
	AccountScope    Scope = "account"
	ConnectionScope Scope = "connection"
)

// Definition is available even when a component's resources are not running.
// Validate must be side-effect free: no resources, goroutines, or configuration writes.
type Definition struct {
	Host     string
	Factory  Factory
	Assets   fs.FS
	Routes   []types.WebRoute
	Manifest manifest.Manifest
	Scope    Scope
	Validate func(context.Context, config.View) error
}

// Definitions describes registered factories without constructing or starting them.
func (r *Registry) Definitions(scope Scope) []Definition {
	result := make([]Definition, 0, len(r.entries))
	for _, entry := range r.entries {
		definition := Definition{Manifest: cloneManifest(entry.Manifest), Scope: scope, Factory: entry.New}
		factory := entry.New
		definition.Validate = func(ctx context.Context, view config.View) error {
			return invoke(func() error {
				if prepared, ok := factory().(PreparedConfig); ok {
					_, err := prepared.PrepareConfig(ctx, view)
					return err
				}
				return ctx.Err()
			})
		}
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Manifest.ID < result[j].Manifest.ID })
	return result
}

type Catalog struct{ entries map[string]Definition }

func NewCatalog(definitions ...Definition) (*Catalog, error) {
	c := &Catalog{entries: make(map[string]Definition)}
	for _, definition := range definitions {
		id := definition.Manifest.ID
		if id == "" {
			return nil, fmt.Errorf("component ID is required")
		}
		if _, exists := c.entries[id]; exists {
			return nil, fmt.Errorf("duplicate component %s", id)
		}
		switch definition.Scope {
		case ProcessScope, AccountScope, ConnectionScope:
		default:
			return nil, fmt.Errorf("%s: invalid component scope", id)
		}
		if _, err := config.New(definition.Manifest.Config, nil); err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		definition.Manifest = cloneManifest(definition.Manifest)
		definition.Routes = append([]types.WebRoute(nil), definition.Routes...)
		c.entries[id] = definition
	}
	return c, nil
}

func (c *Catalog) Definitions() []Definition {
	result := make([]Definition, 0, len(c.entries))
	for _, d := range c.entries {
		d.Manifest = cloneManifest(d.Manifest)
		d.Routes = append([]types.WebRoute(nil), d.Routes...)
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Manifest.ID < result[j].Manifest.ID })
	return result
}

func (c *Catalog) View(ctx context.Context, id string, values map[string]any) (config.View, error) {
	if err := ctx.Err(); err != nil {
		return config.View{}, err
	}
	d, ok := c.entries[id]
	if !ok {
		return config.View{}, fmt.Errorf("unknown component %s", id)
	}
	view, err := config.New(d.Manifest.Config, values)
	if err == nil && d.Validate != nil {
		err = invoke(func() error { return d.Validate(ctx, view) })
	}
	if err != nil {
		return config.View{}, fmt.Errorf("%s: %w", id, err)
	}
	return view, nil
}

func cloneManifest(m manifest.Manifest) manifest.Manifest {
	m.Provides = append([]manifest.Port(nil), m.Provides...)
	m.Requires = append([]manifest.Require(nil), m.Requires...)
	m.Publishes = append([]string(nil), m.Publishes...)
	m.Subscribes = append([]string(nil), m.Subscribes...)
	m.Commands = append([]types.ConsoleCommand(nil), m.Commands...)
	for i := range m.Commands {
		m.Commands[i].Aliases = append([]string(nil), m.Commands[i].Aliases...)
	}
	m.Pages = append([]manifest.Page(nil), m.Pages...)
	for i := range m.Pages {
		m.Pages[i].Settings = append([]string(nil), m.Pages[i].Settings...)
	}
	// Defaults and bounds must not be shared with a caller or an HMI response.
	data, _ := json.Marshal(m.Config)
	fields := []manifest.ConfigField{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&fields)
	if fields == nil {
		fields = []manifest.ConfigField{}
	}
	m.Config = fields
	return m
}
