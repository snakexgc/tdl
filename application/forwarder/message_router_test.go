package forwarder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	routingTargetFirst  = "chat:2"
	routingTargetSecond = "chat:3"
	routingMissingPeer  = "missing"
)

type routingPeersFunc func(context.Context, types.AccountID, string, bool) (types.ForwardPeer, error)

func (f routingPeersFunc) ResolveForwardPeer(ctx context.Context, account types.AccountID, raw string, comments bool) (types.ForwardPeer, error) {
	return f(ctx, account, raw, comments)
}

type listeningFunc func(context.Context, types.AccountID) (types.ForwardListening, error)

func (f listeningFunc) Listening(ctx context.Context, account types.AccountID) (types.ForwardListening, error) {
	return f(ctx, account)
}

type routingRulesFunc func(context.Context, types.ChatRef) []types.ForwardDestination

func (f routingRulesFunc) Destinations(ctx context.Context, source types.ChatRef) []types.ForwardDestination {
	return f(ctx, source)
}

func routingMessage() types.ForwardMessage {
	return types.ForwardMessage{Account: types.DefaultAccount, Peer: types.MessagePeer{Kind: forwardPeerChannel, ID: 1}, MessageID: 10, GroupedID: 42, Automatic: true}
}

func defaultRoutingOptions() RoutingOptions {
	return RoutingOptions{
		Peers: routingPeersFunc(func(_ context.Context, _ types.AccountID, raw string, _ bool) (types.ForwardPeer, error) {
			if raw == routingMissingPeer {
				return types.ForwardPeer{}, errors.New("peer missing")
			}
			return types.ForwardPeer{Reference: types.ChatRef(raw), LinkedDiscussion: "channel:1"}, nil
		}),
		Listening: listeningFunc(func(context.Context, types.AccountID) (types.ForwardListening, error) {
			return types.ForwardListening{Sources: []string{"channel:100"}, Comments: true}, nil
		}),
	}
}

func TestMessageRouterExplicitFanoutSurvivesPartialFailureAndMissingDefault(t *testing.T) {
	ctx := context.Background()
	q := NewQueue(&partialCommandRepository{memoryRepository: memoryRepository{jobs: map[string]Job{}}})
	p := defaultPolicy()
	p.command.Target = routingMissingPeer
	q.configuration.Store(&p)
	options := defaultRoutingOptions()
	options.Rules = routingRulesFunc(func(context.Context, types.ChatRef) []types.ForwardDestination {
		return []types.ForwardDestination{{Target: routingTargetFirst, Mode: forwardModeDefault}, {Target: routingTargetSecond, Mode: forwardModeClone, Silent: true}}
	})
	options.Peers = routingPeersFunc(func(context.Context, types.AccountID, string, bool) (types.ForwardPeer, error) {
		t.Error("explicit routes must not resolve default-route peers")
		return types.ForwardPeer{}, errors.New(routingMissingPeer)
	})
	router := NewMessageRouter(ctx, types.DefaultAccount, q, options)
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	message := routingMessage()
	wanted, err := router.Interested(ctx, message.Account, message.Peer)
	require.NoError(t, err)
	require.True(t, wanted)
	require.ErrorContains(t, router.SubmitMessage(ctx, message), "storage full")
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	message.MessageID++ // The next update for the same album retries only the failed target.
	require.NoError(t, router.SubmitMessage(ctx, message))
	jobs, err = q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	for _, job := range jobs {
		require.Equal(t, forwardPeerChannel, job.SourcePeerKind)
		require.NotEqual(t, p.command.Target, job.Destination)
	}
}

func TestMessageRouterOwnsTypedListeningDefaultRouteDedupeAndLiveDefaults(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	p := defaultPolicy()
	p.command.Target = routingTargetFirst
	q.configuration.Store(&p)
	router := NewMessageRouter(ctx, types.DefaultAccount, q, defaultRoutingOptions())
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	message := routingMessage()
	wanted, err := router.Interested(ctx, message.Account, message.Peer)
	require.NoError(t, err)
	require.True(t, wanted, "linked discussion is a selected source")
	other := message.Peer
	other.Kind = forwardPeerUser
	wanted, err = router.Interested(ctx, message.Account, other)
	require.NoError(t, err)
	require.False(t, wanted, "equal numeric IDs must not match another peer type")
	var requests sync.WaitGroup
	for range 8 {
		requests.Go(func() { require.NoError(t, router.SubmitMessage(ctx, message)) })
	}
	requests.Wait()
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, forwardPeerChannel, jobs[0].SourcePeerKind)
	// Reaction forwarding ignores the automatic listen set, but not source type.
	message.Peer = other
	message.Automatic = false
	require.NoError(t, router.SubmitMessage(ctx, message))
	jobs, err = q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	next := p
	next.command = CommandSettings{Target: routingTargetSecond, Mode: forwardModeClone, Silent: true}
	q.configuration.Store(&next)
	require.NoError(t, router.SubmitMessage(ctx, message))
	jobs, err = q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	old, updated := 0, 0
	for _, job := range jobs {
		if job.Destination == p.command.Target {
			old++
			require.False(t, job.Silent)
		} else {
			updated++
			require.Equal(t, forwardPeerUser, job.SourcePeerKind)
			require.Equal(t, forwardModeClone, job.Mode)
			require.True(t, job.Silent)
		}
	}
	require.Equal(t, 2, old)
	require.Equal(t, 1, updated)
	invalid := next
	invalid.command.Target = routingMissingPeer
	q.configuration.Store(&invalid)
	require.ErrorContains(t, router.SubmitMessage(ctx, message), "peer missing")
	jobs, err = q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 3, "failed new target cannot reuse previous target")
}

func TestMessageRouterStopDrainsResolutionAndRejectsOldHandle(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	options := defaultRoutingOptions()
	options.Peers = routingPeersFunc(func(ctx context.Context, _ types.AccountID, raw string, _ bool) (types.ForwardPeer, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return types.ForwardPeer{Reference: types.ChatRef(raw)}, nil
	})
	router := NewMessageRouter(ctx, types.DefaultAccount, q, options)
	message := routingMessage()
	message.Account = "other"
	require.ErrorContains(t, router.SubmitMessage(ctx, message), "account mismatch")
	message.Account, message.Automatic = types.DefaultAccount, false
	exited := make(chan error, 1)
	go func() { exited <- router.SubmitMessage(ctx, message) }()
	<-entered
	bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, router.Stop(bounded), context.DeadlineExceeded)
	<-canceled
	close(release)
	require.ErrorIs(t, <-exited, context.Canceled)
	require.NoError(t, router.Stop(ctx))
	require.ErrorContains(t, router.SubmitMessage(ctx, message), "stopped")
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestMessageRouterFailedDefaultRouteAdmissionCanBeRetriedAndTTLExpires(t *testing.T) {
	ctx := context.Background()
	q := NewQueue(&partialCommandRepository{memoryRepository: memoryRepository{jobs: map[string]Job{}}})
	p := defaultPolicy()
	p.command.Target = routingTargetFirst
	p.dedupe = time.Millisecond
	q.configuration.Store(&p)
	router := NewMessageRouter(ctx, types.DefaultAccount, q, defaultRoutingOptions())
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	message := routingMessage()
	require.NoError(t, router.SubmitMessage(ctx, message))
	message.GroupedID++
	require.ErrorContains(t, router.SubmitMessage(ctx, message), "storage full")
	require.NoError(t, router.SubmitMessage(ctx, message))
	require.Eventually(t, func() bool {
		if err := router.SubmitMessage(ctx, message); err != nil {
			t.Error(fmt.Errorf("retry default-route message: %w", err))
			return false
		}
		jobs, err := q.List(ctx)
		return err == nil && len(jobs) >= 3
	}, time.Second, 5*time.Millisecond)
}

func TestMessageRouterDiscussionFailureDoesNotDisableParentSource(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	p := defaultPolicy()
	p.command.Target = routingTargetFirst
	q.configuration.Store(&p)
	options := defaultRoutingOptions()
	options.Peers = routingPeersFunc(func(_ context.Context, _ types.AccountID, raw string, comments bool) (types.ForwardPeer, error) {
		peer := types.ForwardPeer{Reference: types.ChatRef(raw)}
		if comments {
			return peer, errors.New("discussion metadata unavailable")
		}
		return peer, nil
	})
	router := NewMessageRouter(ctx, types.DefaultAccount, q, options)
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	wanted, err := router.Interested(ctx, types.DefaultAccount, types.MessagePeer{Kind: forwardPeerChannel, ID: 100})
	require.NoError(t, err)
	require.True(t, wanted)
	wanted, err = router.Interested(ctx, types.DefaultAccount, types.MessagePeer{Kind: forwardPeerChannel, ID: 1})
	require.ErrorContains(t, err, "discussion metadata unavailable")
	require.False(t, wanted)
}

func TestMessageRouterUsesCurrentPolicyForEachAdmission(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	settings := defaultPolicy()
	settings.command = CommandSettings{Target: routingTargetFirst, Mode: forwardModeDefault}
	q.configuration.Store(&settings)
	options := defaultRoutingOptions()
	router := NewMessageRouter(ctx, types.DefaultAccount, q, options)
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	message := routingMessage()
	require.NoError(t, router.SubmitMessage(ctx, message))
	next := settings
	next.command = CommandSettings{Target: routingTargetSecond, Mode: forwardModeClone, Silent: true}
	q.configuration.Store(&next)
	require.NoError(t, router.SubmitMessage(ctx, message))
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	seen := map[string]Job{}
	for _, job := range jobs {
		seen[job.Destination] = job
	}
	require.False(t, seen[routingTargetFirst].Silent)
	require.True(t, seen[routingTargetSecond].Silent)
	require.Equal(t, forwardModeClone, seen[routingTargetSecond].Mode)
	invalid := next
	invalid.command.Mode = "invalid"
	q.configuration.Store(&invalid)
	require.ErrorContains(t, router.SubmitMessage(ctx, message), "invalid forward mode")
}

type blockedRoutingRepository struct {
	memoryRepository
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *blockedRoutingRepository) Save(ctx context.Context, job Job) error {
	first := false
	r.once.Do(func() { first = true })
	if first {
		close(r.entered)
		<-r.release
		return errors.New("first persistence failed")
	}
	return r.memoryRepository.Save(ctx, job)
}

func TestConcurrentDefaultRouteDuplicateWaitsForDurableAdmission(t *testing.T) {
	ctx := context.Background()
	store := &blockedRoutingRepository{memoryRepository: memoryRepository{jobs: map[string]Job{}}, entered: make(chan struct{}), release: make(chan struct{})}
	q := NewQueue(store)
	p := defaultPolicy()
	p.command.Target = routingTargetFirst
	q.configuration.Store(&p)
	router := NewMessageRouter(ctx, types.DefaultAccount, q, defaultRoutingOptions())
	t.Cleanup(func() { require.NoError(t, router.Stop(ctx)) })
	message := routingMessage()
	first, second := make(chan error, 1), make(chan error, 1)
	releaseStore := sync.OnceFunc(func() { close(store.release) })
	t.Cleanup(releaseStore)
	go func() { first <- router.SubmitMessage(ctx, message) }()
	<-store.entered
	go func() { second <- router.SubmitMessage(ctx, message) }()
	select {
	case err := <-second:
		t.Fatalf("duplicate returned before the first admission persisted: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseStore()
	require.ErrorContains(t, <-first, "first persistence failed")
	require.NoError(t, <-second)
	jobs, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
}
