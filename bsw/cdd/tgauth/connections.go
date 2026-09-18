package tgauth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/types"
)

// Connections owns authenticated transports for one application lifetime.
// Temporary login transports are isolated until their sessions are committed.
type Connections struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	accounts  map[types.AccountID]*Connection
	replacing map[types.AccountID]bool
	changes   sync.WaitGroup
	logins    map[types.AccountID]bool
}

func NewConnections(ctx context.Context) *Connections {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Connections{ctx: ctx, cancel: cancel, accounts: map[types.AccountID]*Connection{}, replacing: map[types.AccountID]bool{}, logins: map[types.AccountID]bool{}}
}

func (o *Connections) Context() context.Context { return o.ctx }

type Connection struct {
	Client                  *telegram.Client
	run                     func(context.Context, func(context.Context) error) error
	owner                   *Connections
	account                 types.AccountID
	key                     string
	mu                      sync.Mutex
	started, closing, ready bool
	cancel                  context.CancelFunc
	ctx                     context.Context
	readyCh, done           chan struct{}
	readyOnce               sync.Once
	err                     error
	users                   sync.WaitGroup
	handlers                map[*connectionHandler]struct{}
	count                   int
}

type connectionHandler struct {
	mu      sync.Mutex
	closed  bool
	active  sync.WaitGroup
	handler telegram.UpdateHandler
}

func (o *Connections) Open(account types.AccountID, key string, factory func(telegram.UpdateHandler) (*telegram.Client, error)) (*Connection, error) {
	if account == "" {
		account = types.DefaultAccount
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.ctx.Err() != nil {
		return nil, errors.New("account connections are stopped")
	}
	if o.replacing[account] {
		return nil, errors.New("account session is being replaced")
	}
	if current := o.accounts[account]; current != nil {
		current.mu.Lock()
		defer current.mu.Unlock()
		if current.closing {
			return nil, errors.New("account connection is stopping")
		}
		if current.key != key {
			return nil, errors.New("account connection settings changed; stop the previous connection first")
		}
		return current, nil
	}
	c := &Connection{owner: o, account: account, key: key, readyCh: make(chan struct{}), done: make(chan struct{}), handlers: map[*connectionHandler]struct{}{}}
	if factory == nil {
		return nil, errors.New("account transport factory is required")
	}
	client, err := factory(telegram.UpdateHandlerFunc(c.dispatch))
	if err != nil {
		return nil, err
	}
	c.Client, c.run = client, client.Run
	o.accounts[account] = c
	return c, nil
}

func (c *Connection) dispatch(ctx context.Context, updates tg.UpdatesClass) error {
	c.mu.Lock()
	handlers := make([]*connectionHandler, 0, len(c.handlers))
	for handler := range c.handlers {
		handlers = append(handlers, handler)
	}
	c.mu.Unlock()
	var result error
	for _, entry := range handlers {
		entry.mu.Lock()
		if entry.closed {
			entry.mu.Unlock()
			continue
		}
		entry.active.Add(1)
		entry.mu.Unlock()
		func() {
			defer entry.active.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					result = errors.Join(result, fmt.Errorf("account update handler panic: %v", recovered))
				}
			}()
			result = errors.Join(result, entry.handler.Handle(ctx, updates))
		}()
	}
	return result
}

func (c *Connection) Run(ctx context.Context, handler telegram.UpdateHandler, fn func(context.Context) error) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return errors.New("account connection is stopping")
	}
	if !c.started {
		c.started = true
		c.ctx, c.cancel = context.WithCancel(c.owner.ctx)
		go c.serve()
	}
	c.count++
	c.users.Add(1)
	var entry *connectionHandler
	if handler != nil {
		entry = &connectionHandler{handler: handler}
		c.handlers[entry] = struct{}{}
	}
	c.mu.Unlock()
	defer func() {
		transportStopped := c.ctx.Err() != nil
		c.release(entry)
		if ctx.Err() == nil && transportStopped {
			<-c.done
			c.mu.Lock()
			transportErr := c.err
			c.mu.Unlock()
			if transportErr != nil && (result == nil || errors.Is(result, context.Canceled)) {
				result = transportErr
			}
		}
	}()
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(c.ctx, cancel)
	defer func() { unlink(); cancel() }()
	select {
	case <-call.Done():
		return call.Err()
	case <-c.readyCh:
	}
	c.mu.Lock()
	ready, err := c.ready, c.err
	c.mu.Unlock()
	if !ready {
		return err
	}
	if err := call.Err(); err != nil {
		return err
	}
	return fn(call)
}

func (c *Connection) serve() {
	err := invokeConnection(c.run, c.ctx, func(ctx context.Context) error {
		c.mu.Lock()
		c.ready = true
		c.mu.Unlock()
		c.readyOnce.Do(func() { close(c.readyCh) })
		<-ctx.Done()
		c.mu.Lock()
		c.closing = true
		c.cancel()
		c.mu.Unlock()
		c.users.Wait()
		return ctx.Err()
	})
	c.mu.Lock()
	if err == nil && !c.ready {
		err = errors.New("account transport stopped before ready")
	}
	c.err, c.closing = err, true
	c.cancel()
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.readyCh) })
	// A transport failure may end Run before its callback; still retain ownership
	// until all callers have observed cancellation and released their resources.
	c.users.Wait()
	c.owner.mu.Lock()
	if c.owner.accounts[c.account] == c {
		delete(c.owner.accounts, c.account)
	}
	c.owner.mu.Unlock()
	close(c.done)
}

func invokeConnection(run func(context.Context, func(context.Context) error) error, ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("account transport panic: %v", recovered)
		}
	}()
	return run(ctx, fn)
}

func (c *Connection) release(entry *connectionHandler) {
	if entry != nil {
		entry.mu.Lock()
		entry.closed = true
		entry.mu.Unlock()
		entry.active.Wait()
	}
	c.mu.Lock()
	delete(c.handlers, entry)
	c.count--
	last := c.count == 0
	if last {
		c.closing = true
		c.cancel()
	}
	c.mu.Unlock()
	c.users.Done()
	if last {
		<-c.done
	}
}

// Drain closes an old session before login commits a replacement, preventing
// late session writes from restoring the old authorization.
func (o *Connections) Drain(ctx context.Context, account types.AccountID) error {
	if o == nil {
		return nil
	}
	if account == "" {
		account = types.DefaultAccount
	}
	o.mu.Lock()
	c := o.accounts[account]
	if c == nil {
		o.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	c.closing = true
	if !c.started {
		delete(o.accounts, account)
		c.mu.Unlock()
		o.mu.Unlock()
		return nil
	}
	c.cancel()
	c.mu.Unlock()
	o.mu.Unlock()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Connections) Stop(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.closed = true
	o.cancel()
	accounts := make([]types.AccountID, 0, len(o.accounts))
	for account := range o.accounts {
		accounts = append(accounts, account)
	}
	o.mu.Unlock()
	var result error
	for _, account := range accounts {
		if err := o.Drain(ctx, account); err != nil {
			result = errors.Join(result, fmt.Errorf("%s: %w", account, err))
		}
	}
	done := make(chan struct{})
	go func() { o.changes.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		result = errors.Join(result, ctx.Err())
	}
	return result
}

// BeginLogin serializes login attempts across Bot and WebUI for the same account
// and binds temporary authentication to the account owner's shutdown.
func (o *Connections) BeginLogin(ctx context.Context, account types.AccountID) (context.Context, func(), error) {
	if o == nil {
		return ctx, func() {}, nil
	}
	if account == "" {
		account = types.DefaultAccount
	}
	o.mu.Lock()
	if o.closed || o.logins[account] || o.replacing[account] {
		o.mu.Unlock()
		return nil, nil, errors.New("another account login is active or the account owner is stopped")
	}
	o.logins[account] = true
	o.changes.Add(1)
	o.mu.Unlock()
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(o.ctx, cancel)
	var once sync.Once
	return call, func() {
		once.Do(func() {
			unlink()
			cancel()
			o.mu.Lock()
			delete(o.logins, account)
			o.mu.Unlock()
			o.changes.Done()
		})
	}, nil
}

func (o *Connections) Replace(ctx context.Context, account types.AccountID, commit func() error) error {
	return o.replace(ctx, account, commit, true)
}

// ReplaceIdle is for session deletion/maintenance; active authentication must
// finish first so a late login commit cannot recreate a deleted session.
func (o *Connections) ReplaceIdle(ctx context.Context, account types.AccountID, commit func() error) error {
	return o.replace(ctx, account, commit, false)
}

func (o *Connections) replace(ctx context.Context, account types.AccountID, commit func() error, loginCommit bool) error {
	if o == nil {
		return commit()
	}
	if account == "" {
		account = types.DefaultAccount
	}
	o.mu.Lock()
	if o.closed || o.replacing[account] || (!loginCommit && o.logins[account]) {
		o.mu.Unlock()
		return errors.New("account session replacement is unavailable")
	}
	o.replacing[account] = true
	o.changes.Add(1)
	o.mu.Unlock()
	defer o.changes.Done()
	defer func() { o.mu.Lock(); delete(o.replacing, account); o.mu.Unlock() }()
	if err := o.Drain(ctx, account); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return errors.New("account connections are stopped")
	}
	return commit()
}
