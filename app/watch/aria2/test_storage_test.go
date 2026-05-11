package aria2

import (
	"context"
	"sync"

	"github.com/iyear/tdl/core/storage"
)

type memoryTaskStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryTaskStorage() *memoryTaskStorage {
	return &memoryTaskStorage{data: map[string][]byte{}}
}

func (m *memoryTaskStorage) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	value, ok := m.data[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m *memoryTaskStorage) Set(ctx context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = append([]byte(nil), value...)
	return nil
}

func (m *memoryTaskStorage) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.data, key)
	return nil
}
