package storage

import (
	"context"
	"sync"
)

// Memory provides isolated, atomic in-memory session and task storage.
// Its zero value is ready for use.
type Memory struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func (m *Memory) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return memoryTransaction(m.values).Get(ctx, key)
}

func (m *Memory) Set(ctx context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string][]byte{}
	}
	return memoryTransaction(m.values).Set(ctx, key, value)
}

func (m *Memory) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return memoryTransaction(m.values).Delete(ctx, key)
}

func (m *Memory) Update(ctx context.Context, fn func(Storage) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	values := make(memoryTransaction, len(m.values))
	for key, value := range m.values {
		values[key] = append([]byte(nil), value...)
	}
	if err := fn(values); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.values = values
	return nil
}

type memoryTransaction map[string][]byte

func (m memoryTransaction) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := m[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m memoryTransaction) Set(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m[key] = append([]byte(nil), value...)
	return nil
}

func (m memoryTransaction) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(m, key)
	return nil
}
