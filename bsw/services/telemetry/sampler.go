// Package telemetry coalesces control-plane observations. Sampling never runs
// on the file transfer path, and the number of viewers does not multiply work.
package telemetry

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type Source func(context.Context) (any, error)

type sample struct {
	mu   sync.Mutex
	at   time.Time
	data json.RawMessage
	err  error
}

type Sampler struct {
	mu       sync.Mutex
	values   map[string]*sample
	interval time.Duration
}

func New(interval time.Duration) *Sampler {
	return &Sampler{values: map[string]*sample{}, interval: interval}
}

func (s *Sampler) Read(ctx context.Context, topic string, source Source) (json.RawMessage, error) {
	s.mu.Lock()
	entry := s.values[topic]
	if entry == nil {
		entry = &sample{}
		s.values[topic] = entry
	}
	s.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if time.Since(entry.at) < s.interval {
		return entry.data, entry.err
	}
	value, err := source(ctx)
	if err == nil {
		entry.data, err = json.Marshal(value)
	}
	entry.err, entry.at = err, time.Now()
	return entry.data, err
}
