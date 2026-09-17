// Package nvm grants scoped access to explicitly owned persistent datasets.
package nvm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/snakexgc/tdl/internal/core/storage"
)

type Dataset struct {
	Name   string
	Writer string
	Prefix string
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
	if dataset.Name == "" || dataset.Writer == "" || dataset.Prefix == "" {
		return fmt.Errorf("dataset name, writer and prefix are required")
	}
	if _, exists := r.datasets[dataset.Name]; exists {
		return fmt.Errorf("duplicate dataset %s", dataset.Name)
	}
	for _, existing := range r.datasets {
		if strings.HasPrefix(dataset.Prefix, existing.Prefix) || strings.HasPrefix(existing.Prefix, dataset.Prefix) {
			return fmt.Errorf("datasets %s and %s overlap", existing.Name, dataset.Name)
		}
	}
	r.datasets[dataset.Name] = dataset
	return nil
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
	return reader{store: r.store, prefix: dataset.Prefix}, nil
}

func (r *Registry) Writer(name, owner string) (storage.Storage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dataset, ok := r.datasets[name]
	if !ok {
		return nil, fmt.Errorf("unknown dataset %s", name)
	}
	if owner != dataset.Writer {
		return nil, fmt.Errorf("%s does not own dataset %s", owner, name)
	}
	return &writer{reader: reader{store: r.store, prefix: dataset.Prefix}}, nil
}

type reader struct {
	store  storage.Storage
	prefix string
}

func (r reader) check(key string) error {
	if r.store == nil {
		return fmt.Errorf("dataset storage is unavailable")
	}
	if !strings.HasPrefix(key, r.prefix) {
		return fmt.Errorf("key is outside dataset scope")
	}
	return nil
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
		return fn(&writer{reader: reader{store: tx, prefix: w.prefix}})
	})
}
