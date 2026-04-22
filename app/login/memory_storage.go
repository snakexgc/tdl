package login

import (
	"context"
	"sync"

	"github.com/iyear/tdl/core/storage"
)

type memoryStorage struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		data: make(map[string][]byte),
	}
}

func (m *memoryStorage) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	value, ok := m.data[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m *memoryStorage) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memoryStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.data, key)
	return nil
}
