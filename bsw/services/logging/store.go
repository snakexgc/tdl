// Package logging retains structured runtime logs independently of business components.
package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const Capacity = 2000

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
	mu         sync.Mutex
	entries    []Entry
	next       int
	sequence   uint64
	writer     io.Writer
	sinkErrors map[string]string
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
	entry = sanitizeEntry(entry)
	s.append(entry)
	if s.writer == nil {
		return nil
	}
	data, err := json.Marshal(entry)
	if err == nil {
		data = append(data, '\n')
		var written int
		written, err = s.writer.Write(data)
		if err == nil && written != len(data) {
			err = io.ErrShortWrite
		}
	}
	s.recordSinkError("events.jsonl", err)
	return err
}

// RecordSinkError publishes failures from other sinks without recursively logging.
// A successful journal write must not erase a text-log failure.
func (s *Store) RecordSinkError(sink string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordSinkError(sink, err)
}

func (s *Store) recordSinkError(sink string, err error) {
	if err == nil {
		delete(s.sinkErrors, sink)
		return
	}
	if s.sinkErrors == nil {
		s.sinkErrors = make(map[string]string)
	}
	s.sinkErrors[sink] = Redact(err.Error())
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
		s.append(sanitizeEntry(entry))
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
	warnings := make([]string, 0, len(s.sinkErrors))
	for sink, message := range s.sinkErrors {
		warnings = append(warnings, sink+": "+message)
	}
	sort.Strings(warnings)
	return result, strings.Join(warnings, "; ")
}

type contextKey struct{}

func sanitizeEntry(entry Entry) Entry {
	entry.Message = Redact(entry.Message)
	entry.Account = Redact(entry.Account)
	entry.Component = Redact(entry.Component)
	entry.Kind = Redact(entry.Kind)
	entry.Logger = Redact(entry.Logger)
	entry.Caller = Redact(entry.Caller)
	if entry.Details != "" {
		var details any
		if json.Valid([]byte(entry.Details)) && decodeJSON([]byte(entry.Details), &details) == nil {
			entry.Details = safeDetails(details)
		} else {
			entry.Details = Redact(entry.Details)
		}
	}
	return entry
}

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
