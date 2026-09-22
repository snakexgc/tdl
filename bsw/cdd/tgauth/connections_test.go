package tgauth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

const connectionAccount types.AccountID = "account"

func TestOnlyOneActiveAccountWhileOtherSessionsCanBeMaintained(t *testing.T) {
	ctx := context.Background()
	owner := NewConnections(ctx)
	t.Cleanup(func() { require.NoError(t, owner.Stop(ctx)) })
	c := testConnection(t, owner)
	_, err := owner.Open("other", "profile", func(telegram.UpdateHandler) (*telegram.Client, error) {
		t.Fatal("second account transport created")
		return nil, nil
	})
	require.ErrorContains(t, err, "another account connection is active")
	_, release, err := owner.BeginLogin(ctx, "other")
	require.NoError(t, err)
	committed := false
	require.NoError(t, owner.Replace(ctx, "other", func() error { committed = true; return nil }))
	release()
	require.True(t, committed)
	require.NoError(t, owner.ReplaceIdle(ctx, "other", func() error { return nil }))
	same, err := owner.Open(connectionAccount, "profile", nil)
	require.NoError(t, err)
	require.Same(t, c, same)
	require.NoError(t, owner.Drain(ctx, connectionAccount))
	next, err := owner.Open("other", "profile", func(telegram.UpdateHandler) (*telegram.Client, error) {
		return telegram.NewClient(1, "test", telegram.Options{}), nil
	})
	require.NoError(t, err)
	require.NotSame(t, c, next)
}

func TestSessionMaintenanceAndLoginCannotOverlap(t *testing.T) {
	owner := NewConnections(context.Background())
	_, release, err := owner.BeginLogin(context.Background(), connectionAccount)
	require.NoError(t, err)
	require.Error(t, owner.ReplaceIdle(context.Background(), connectionAccount, func() error { t.Fatal("deleted during login"); return nil }))
	release()
	entered, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- owner.ReplaceIdle(context.Background(), connectionAccount, func() error { close(entered); <-finish; return nil })
	}()
	<-entered
	_, _, err = owner.BeginLogin(context.Background(), connectionAccount)
	close(finish)
	require.Error(t, err)
	require.NoError(t, <-done)
	_, release, err = owner.BeginLogin(context.Background(), connectionAccount)
	require.NoError(t, err)
	release()
	require.NoError(t, owner.Stop(context.Background()))
}

func testConnection(t *testing.T, owner *Connections) *Connection {
	t.Helper()
	c, err := owner.Open(connectionAccount, "profile", func(telegram.UpdateHandler) (*telegram.Client, error) {
		return telegram.NewClient(1, "test", telegram.Options{}), nil
	})
	require.NoError(t, err)
	c.run = func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
	return c
}

func TestAccountConnectionSharesTransportAndDrainsAllUsers(t *testing.T) {
	owner := NewConnections(context.Background())
	c := testConnection(t, owner)
	var runs atomic.Int32
	c.run = func(ctx context.Context, fn func(context.Context) error) error { runs.Add(1); return fn(ctx) }
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- c.Run(context.Background(), nil, func(ctx context.Context) error { close(entered); <-ctx.Done(); <-release; return ctx.Err() })
	}()
	<-entered
	same, err := owner.Open(connectionAccount, "profile", func(telegram.UpdateHandler) (*telegram.Client, error) {
		t.Error("second client created")
		return nil, nil
	})
	require.NoError(t, err)
	require.Same(t, c, same)
	require.NoError(t, same.Run(context.Background(), nil, func(context.Context) error { return nil }))
	require.EqualValues(t, 1, runs.Load())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, owner.Stop(ctx), context.DeadlineExceeded)
	_, err = owner.Open(connectionAccount, "profile", nil)
	require.Error(t, err)
	close(release)
	require.ErrorIs(t, <-first, context.Canceled)
	require.NoError(t, owner.Stop(context.Background()))
}

func TestSessionReplacementBlocksNewUsersUntilOldTransportStops(t *testing.T) {
	owner := NewConnections(context.Background())
	c := testConnection(t, owner)
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- c.Run(context.Background(), nil, func(ctx context.Context) error { close(entered); <-ctx.Done(); <-release; return ctx.Err() })
	}()
	<-entered
	committing, commitRelease := make(chan struct{}), make(chan struct{})
	replaced := make(chan error, 1)
	go func() {
		replaced <- owner.Replace(context.Background(), connectionAccount, func() error { close(committing); <-commitRelease; return nil })
	}()
	require.Eventually(t, func() bool { owner.mu.Lock(); defer owner.mu.Unlock(); return owner.replacing[connectionAccount] }, time.Second, time.Millisecond)
	_, err := owner.Open(connectionAccount, "profile", nil)
	require.ErrorContains(t, err, "being replaced")
	select {
	case <-committing:
		t.Fatal("session committed before old user drained")
	default:
	}
	close(release)
	require.ErrorIs(t, <-first, context.Canceled)
	<-committing
	_, err = owner.Open(connectionAccount, "profile", nil)
	require.ErrorContains(t, err, "being replaced")
	close(commitRelease)
	require.NoError(t, <-replaced)
	next := testConnection(t, owner)
	require.NotSame(t, c, next)
	require.NoError(t, next.Run(context.Background(), nil, func(context.Context) error { return nil }))
	require.NoError(t, owner.Stop(context.Background()))
}

func TestTransportInternalCancellationCancelsSharedUsers(t *testing.T) {
	owner := NewConnections(context.Background())
	c := testConnection(t, owner)
	internalCancel := make(chan context.CancelFunc, 1)
	c.run = func(ctx context.Context, fn func(context.Context) error) error {
		inner, cancel := context.WithCancel(ctx)
		defer cancel()
		internalCancel <- cancel
		return fn(inner)
	}
	finished := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		finished <- c.Run(context.Background(), nil, func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() })
	}()
	<-entered
	(<-internalCancel)()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("shared user did not observe transport cancellation")
	}
	require.NoError(t, owner.Stop(context.Background()))
}

func TestLoginAdmissionIsSharedAndShutdownWaitsForTemporarySession(t *testing.T) {
	owner := NewConnections(context.Background())
	loginCtx, release, err := owner.BeginLogin(context.Background(), connectionAccount)
	require.NoError(t, err)
	_, _, err = owner.BeginLogin(context.Background(), connectionAccount)
	require.Error(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, owner.Stop(ctx), context.DeadlineExceeded)
	require.ErrorIs(t, loginCtx.Err(), context.Canceled)
	release()
	release()
	require.NoError(t, owner.Stop(context.Background()))
}

func TestFatalTransportErrorReachesWatcherForRecovery(t *testing.T) {
	owner := NewConnections(context.Background())
	c := testConnection(t, owner)
	failure := errors.New("transport disconnected")
	trigger := make(chan context.CancelFunc, 1)
	c.run = func(ctx context.Context, fn func(context.Context) error) error {
		inner, cancel := context.WithCancel(ctx)
		defer cancel()
		trigger <- cancel
		_ = fn(inner)
		return failure
	}
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- c.Run(context.Background(), nil, func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() })
	}()
	<-entered
	(<-trigger)()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, failure)
	case <-time.After(time.Second):
		t.Fatal("transport failure was not propagated")
	}
	require.NoError(t, owner.Stop(context.Background()))
}
