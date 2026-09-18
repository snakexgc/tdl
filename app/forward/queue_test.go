package forward

import (
	"context"
	"sync"

	"github.com/snakexgc/tdl/internal/core/storage"
)

// memStorage is an in-memory storage.Storage for queue tests.
type memStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStorage() *memStorage { return &memStorage{data: map[string][]byte{}} }

func (m *memStorage) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (m *memStorage) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}
