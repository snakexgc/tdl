// Package eventbus routes small control-plane events. File bytes and frequent
// progress samples belong on synchronous ports, never on this bus.
package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/snakexgc/tdl/interfaces/types"
)

var (
	ErrClosed = errors.New("event bus is closed")
	ErrFull   = errors.New("subscriber queue is full")
)

type Event struct {
	Account types.AccountID
	Topic   string
	Payload json.RawMessage
}

type (
	Handler  func(context.Context, Event) error
	Reporter func(string, error)
)

type subscriber struct {
	account types.AccountID
	topic   string
	queue   chan Event
	ctx     context.Context
	cancel  context.CancelFunc
	owner   <-chan struct{}
	done    chan struct{}
}

type Bus struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	closed bool
	wg     sync.WaitGroup
}

func New() *Bus { return &Bus{subs: make(map[*subscriber]struct{})} }

// Subscribe isolates handlers behind independent bounded queues. Cancellation
// releases the subscription; handlers must obey their context.
func (b *Bus) Subscribe(ctx context.Context, account types.AccountID, topic string, capacity int, handler Handler, report Reporter) (func(), error) {
	if account == "" || topic == "" || capacity < 1 || handler == nil {
		return nil, errors.New("account, topic, positive capacity and handler are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner := ctx.Done()
	ctx, cancel := context.WithCancel(ctx)
	s := &subscriber{account: account, topic: topic, queue: make(chan Event, capacity), ctx: ctx, cancel: cancel, owner: owner, done: make(chan struct{})}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		cancel()
		return nil, ErrClosed
	}
	b.subs[s] = struct{}{}
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		defer cancel()
		defer func() { b.mu.Lock(); delete(b.subs, s); close(s.done); b.mu.Unlock() }()
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-s.queue:
				if ctx.Err() != nil {
					return
				}
				if err := invoke(func() error { return handler(ctx, event) }); err != nil && report != nil {
					_ = invoke(func() error { report(topic, err); return nil })
				}
			}
		}
	}()
	return cancel, nil
}

// Publish either enqueues to every active matching subscriber or to none when
// one queue is full. It never blocks on application handlers. Each recipient
// owns its payload bytes. Events are delivered in publish order per subscriber.
func (b *Bus) Publish(ctx context.Context, account types.AccountID, topic string, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if account == "" || topic == "" {
		return errors.New("event account and topic are required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	var recipients []*subscriber
	for s := range b.subs {
		if s.account == account && s.topic == topic && s.ctx.Err() == nil {
			if len(s.queue) == cap(s.queue) {
				return ErrFull
			}
			recipients = append(recipients, s)
		}
	}
	for _, s := range recipients {
		s.queue <- Event{Account: account, Topic: topic, Payload: bytes.Clone(data)}
	}
	return nil
}

func (b *Bus) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	for s := range b.subs {
		s.cancel()
	}
	b.mu.Unlock()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitScope drains handlers created with a particular lifecycle context. The
// caller cancels that lifecycle first; unrelated components keep running.
func (b *Bus) WaitScope(ctx context.Context, owner <-chan struct{}) error {
	b.mu.Lock()
	var pending []<-chan struct{}
	for s := range b.subs {
		if s.owner == owner {
			pending = append(pending, s.done)
		}
	}
	b.mu.Unlock()
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func invoke(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("event handler panic: %v", recovered)
		}
	}()
	return fn()
}
