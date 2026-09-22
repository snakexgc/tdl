// Package nvm grants scoped access to explicitly owned persistent datasets.
package nvm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/snakexgc/tdl/internal/core/storage"
)

type Dataset struct {
	Name   string
	Writer string
	Prefix string
	// Keys grants exact protocol keys without also granting similarly named data.
	// A dataset declares either Prefix or Keys.
	Keys []string
}

// Registry belongs to one opened storage namespace. Account selection happens
// before constructing it; a capability cannot switch to another namespace.
type Registry struct {
	mu       sync.RWMutex
	store    storage.Storage
	datasets map[string]Dataset
}

func New(store storage.Storage) *Registry {
	return &Registry{store: store, datasets: make(map[string]Dataset)}
}

func (r *Registry) Register(dataset Dataset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dataset.Name == "" || dataset.Writer == "" || (dataset.Prefix == "") == (len(dataset.Keys) == 0) {
		return fmt.Errorf("dataset name, writer and either prefix or exact keys are required")
	}
	dataset.Keys = slices.Clone(dataset.Keys)
	seen := make(map[string]bool, len(dataset.Keys))
	for _, key := range dataset.Keys {
		if key == "" || seen[key] {
			return fmt.Errorf("empty or duplicate dataset key")
		}
		seen[key] = true
	}
	if _, exists := r.datasets[dataset.Name]; exists {
		return fmt.Errorf("duplicate dataset %s", dataset.Name)
	}
	for _, existing := range r.datasets {
		if overlaps(dataset, existing) {
			return fmt.Errorf("datasets %s and %s overlap", existing.Name, dataset.Name)
		}
	}
	r.datasets[dataset.Name] = dataset
	return nil
}

func (d Dataset) contains(key string) bool {
	return (d.Prefix != "" && strings.HasPrefix(key, d.Prefix)) || slices.Contains(d.Keys, key)
}

func overlaps(a, b Dataset) bool {
	if a.Prefix != "" && b.Prefix != "" {
		return strings.HasPrefix(a.Prefix, b.Prefix) || strings.HasPrefix(b.Prefix, a.Prefix)
	}
	for _, key := range a.Keys {
		if b.contains(key) {
			return true
		}
	}
	for _, key := range b.Keys {
		if a.contains(key) {
			return true
		}
	}
	return false
}

type Reader interface {
	Get(context.Context, string) ([]byte, error)
}

func (r *Registry) Reader(name string) (Reader, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dataset, ok := r.datasets[name]
	if !ok {
		return nil, fmt.Errorf("unknown dataset %s", name)
	}
	return reader{store: r.store, scopes: []Dataset{dataset}}, nil
}

func (r *Registry) Writer(name, owner string) (storage.Storage, error) {
	return r.WriterSet(owner, name)
}

// WriterSet grants one owner a union of named datasets while retaining a single
// underlying transaction. Every requested dataset must belong to that owner.
func (r *Registry) WriterSet(owner string, names ...string) (storage.Storage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one dataset is required")
	}
	scopes := make([]Dataset, 0, len(names))
	for _, name := range names {
		dataset, ok := r.datasets[name]
		if !ok {
			return nil, fmt.Errorf("unknown dataset %s", name)
		}
		if owner != dataset.Writer {
			return nil, fmt.Errorf("%s does not own dataset %s", owner, name)
		}
		scopes = append(scopes, dataset)
	}
	return &writer{reader: reader{store: r.store, scopes: scopes}}, nil
}

type reader struct {
	store  storage.Storage
	scopes []Dataset
}

func (r reader) check(key string) error {
	if r.store == nil {
		return fmt.Errorf("dataset storage is unavailable")
	}
	for _, scope := range r.scopes {
		if scope.contains(key) {
			return nil
		}
	}
	return fmt.Errorf("key is outside dataset scope")
}

func (r reader) Get(ctx context.Context, key string) ([]byte, error) {
	if err := r.check(key); err != nil {
		return nil, err
	}
	return r.store.Get(ctx, key)
}

type writer struct{ reader }

func (w *writer) Set(ctx context.Context, key string, value []byte) error {
	if err := w.check(key); err != nil {
		return err
	}
	return w.store.Set(ctx, key, value)
}

func (w *writer) Delete(ctx context.Context, key string) error {
	if err := w.check(key); err != nil {
		return err
	}
	return w.store.Delete(ctx, key)
}

func (w *writer) Update(ctx context.Context, fn func(storage.Storage) error) error {
	return storage.Update(ctx, w.store, func(tx storage.Storage) error {
		return fn(&writer{reader: reader{store: tx, scopes: w.scopes}})
	})
}
