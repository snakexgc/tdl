package bot

import (
	"context"
	"errors"
	"sync"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/dcpool"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/tclient"
	"github.com/snakexgc/tdl/pkg/config"
)

type botMediaSession struct {
	ctx     context.Context
	cancel  context.CancelFunc
	serve   func(context.Context, func(dcpool.Pool)) error
	mu      sync.Mutex
	current *mediaConnection
	workers sync.WaitGroup
}

type mediaConnection struct {
	ready chan struct{}
	pool  *httpdl.BotMediaConnection
	err   error
}

func newBotMediaSession(parent context.Context, opts Options, account types.AccountID, botID int64) *botMediaSession {
	ctx, cancel := context.WithCancel(parent)
	return &botMediaSession{ctx: ctx, cancel: cancel, serve: func(ctx context.Context, publish func(dcpool.Pool)) error {
		if opts.Credentials == nil {
			return errors.New("bot media API credentials unavailable")
		}
		credentials, err := opts.Credentials.Resolve(ctx, account)
		if err != nil {
			return err
		}
		// This client owns an ephemeral bot authorization. The user session store
		// and the account connection manager must never receive this login.
		client, err := tclient.New(logctx.With(ctx, zap.NewNop()), tclient.Options{
			AppID: credentials.App.AppID, AppHash: credentials.App.AppHash, Proxy: opts.Proxy,
			ReconnectTimeout: opts.ReconnectTimeout,
		})
		if err != nil {
			return errors.New("could not create bot media client")
		}
		return client.Run(ctx, func(ctx context.Context) error {
			auth, err := client.Auth().Bot(ctx, opts.Token)
			if err != nil {
				return errors.New("bot media authentication failed")
			}
			user, ok := auth.User.(*tg.User)
			if !ok || !user.Bot || user.ID != botID {
				return errors.New("bot media identity mismatch")
			}
			pool := dcpool.NewPool(client, int64(max(1, config.From(ctx).PoolSize)))
			defer pool.Close()
			publish(pool)
			<-ctx.Done()
			return ctx.Err()
		})
	}}
}

// All concurrent ranges share a single login. A failed run can be opened
// again by a later request, and a caller can leave while the login is pending.
func (s *botMediaSession) pool(ctx context.Context) (*httpdl.BotMediaConnection, error) {
	s.mu.Lock()
	if err := s.ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	c := s.current
	if c == nil {
		c = &mediaConnection{ready: make(chan struct{})}
		s.current = c
		s.workers.Add(1)
		go s.run(c)
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case <-c.ready:
		return c.pool, c.err
	}
}

func (s *botMediaSession) run(c *mediaConnection) {
	defer s.workers.Done()
	err := s.serve(s.ctx, func(pool dcpool.Pool) {
		c.pool = httpdl.NewBotMediaConnection(pool)
		close(c.ready)
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.pool == nil {
		c.err = err
		if c.err == nil {
			c.err = errors.New("bot media session stopped before login")
		}
		close(c.ready)
	}
	s.current = nil
}

func (s *botMediaSession) close() {
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.workers.Wait()
}
