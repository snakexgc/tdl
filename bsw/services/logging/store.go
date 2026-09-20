// Package logging retains structured runtime logs independently of business components.
package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

const Capacity = 5000

type Entry struct {
	ID        uint64    `json:"id"`
	At        time.Time `json:"at"`
	Level     string    `json:"level"`
	Account   string    `json:"account,omitempty"`
	Component string    `json:"component"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
	Logger    string    `json:"logger,omitempty"`
	Caller    string    `json:"caller,omitempty"`
}

type Store struct {
	mu        sync.Mutex
	entries   []Entry
	next      int
	sequence  uint64
	writer    io.Writer
	lastError string
}

func New(writer io.Writer) *Store { return &Store{writer: writer} }

func (s *Store) append(entry Entry) {
	if len(s.entries) < Capacity {
		s.entries = append(s.entries, entry)
	} else {
		s.entries[s.next] = entry
		s.next = (s.next + 1) % Capacity
	}
}

func (s *Store) Write(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	entry.ID = s.sequence
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	entry.At = entry.At.UTC()
	entry.Message = Redact(entry.Message)
	if entry.Details != "" {
		var details any
		if json.Unmarshal([]byte(entry.Details), &details) == nil {
			data, err := json.Marshal(safeValue("", details))
			if err == nil {
				entry.Details = bounded(string(data), 16384)
			}
		} else {
			entry.Details = Redact(entry.Details)
		}
	}
	s.append(entry)
	if s.writer == nil {
		return nil
	}
	data, err := json.Marshal(entry)
	if err == nil {
		_, err = s.writer.Write(append(data, '\n'))
	}
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
	}
	return err
}

// Restore reads only the bounded active journal; rotated archives stay on disk.
func (s *Store) Restore(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scan := bufio.NewScanner(io.LimitReader(file, 12<<20))
	scan.Buffer(make([]byte, 4096), 1<<20)
	s.mu.Lock()
	defer s.mu.Unlock()
	for scan.Scan() {
		var entry Entry
		if json.Unmarshal(scan.Bytes(), &entry) != nil {
			continue
		}
		if entry.ID > s.sequence {
			s.sequence = entry.ID
		}
		s.append(entry)
	}
	return scan.Err()
}

// Snapshot returns newest first, without exposing the store's backing array.
func (s *Store) Snapshot() ([]Entry, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Entry, 0, len(s.entries))
	for i := len(s.entries) - 1; i >= 0; i-- {
		index := i
		if len(s.entries) == Capacity {
			index = (s.next + i) % Capacity
		}
		result = append(result, s.entries[index])
	}
	return result, s.lastError
}

type contextKey struct{}

func WithStore(ctx context.Context, store *Store) context.Context {
	return context.WithValue(ctx, contextKey{}, store)
}

func From(ctx context.Context) *Store {
	if ctx == nil {
		return nil
	}
	store, _ := ctx.Value(contextKey{}).(*Store)
	return store
}
