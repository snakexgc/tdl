// Package dem retains a bounded, concurrency-safe history of diagnostic events.
package dem

import (
	"log/slog"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

type Store struct {
	mu       sync.Mutex
	account  types.AccountID
	capacity int
	sequence uint64
	events   []types.DiagnosticEvent
}

func New(account types.AccountID, capacity int) *Store {
	if capacity < 1 {
		capacity = 128
	}
	return &Store{account: account, capacity: capacity}
}

func (s *Store) Report(component, operation string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	event := types.DiagnosticEvent{Sequence: s.sequence, Account: s.account, Component: component, Operation: operation, Message: err.Error(), At: time.Now()}
	slog.Error("组件诊断事件", "kind", "diagnostic", "component", component, "account", s.account, "operation", operation, "error", err)
	if len(s.events) == s.capacity {
		copy(s.events, s.events[1:])
		s.events[len(s.events)-1] = event
	} else {
		s.events = append(s.events, event)
	}
}

func (s *Store) Events() []types.DiagnosticEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.DiagnosticEvent{}, s.events...)
}
