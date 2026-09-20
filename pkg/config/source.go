package config

import (
	"context"
	"sync/atomic"
)

// Source is an explicitly scoped runtime snapshot for protocol adapters.
// SWCs use their own ConfigView; snapshots persist through the installed configuration repository.
type (
	Source           struct{ value atomic.Pointer[Config] }
	sourceContextKey struct{}
)

func NewSource(value *Config) *Source {
	s := &Source{}
	s.Replace(value)
	return s
}

func (s *Source) Replace(value *Config) {
	copy, err := Clone(value)
	if err != nil {
		panic(err)
	} // Config contains only serializable data.
	s.value.Store(copy)
}

func WithSource(ctx context.Context, source *Source) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, source)
}

func InheritSource(ctx, parent context.Context) context.Context {
	if parent != nil {
		if source, ok := parent.Value(sourceContextKey{}).(*Source); ok {
			return WithSource(ctx, source)
		}
	}
	return ctx
}

func From(ctx context.Context) *Config {
	if ctx != nil {
		if source, ok := ctx.Value(sourceContextKey{}).(*Source); ok {
			copy, err := Clone(source.value.Load())
			if err != nil {
				panic(err)
			}
			return copy
		}
	}
	return Get()
}
