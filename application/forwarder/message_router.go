package forwarder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	forwardModeClone   = "clone"
	forwardPeerChannel = "channel"
	forwardPeerUser    = "user"
)

type RoutingOptions struct {
	Rules     ports.ForwardRules
	Peers     ports.ForwardPeers
	Listening ports.ForwardListening
}

type peerLookup struct {
	peer    types.ForwardPeer
	err     error
	expires time.Time
}

type forwardClaim struct {
	expires time.Time
	done    chan struct{}
}

// MessageRouter owns automatic/default route selection and default-route TTL dedupe.
// The queue retains durable explicit-rule identities; each connection owns one
// router handle, and stopping it drains source resolution and admissions.
type MessageRouter struct {
	account types.AccountID
	queue   *Queue
	options RoutingOptions
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	active  int
	drained chan struct{}
	peerMu  sync.Mutex
	peers   map[string]peerLookup
	claimMu sync.Mutex
	claims  map[string]*forwardClaim
}

func NewMessageRouter(ctx context.Context, account types.AccountID, queue *Queue, options RoutingOptions) *MessageRouter {
	ctx, cancel := context.WithCancel(ctx)
	return &MessageRouter{account: account, queue: queue, options: options, ctx: ctx, cancel: cancel, drained: make(chan struct{}), peers: map[string]peerLookup{}, claims: map[string]*forwardClaim{}}
}

func (r *MessageRouter) begin(ctx context.Context, account types.AccountID) (context.Context, func(), error) {
	r.mu.Lock()
	if r.closed || r.ctx.Err() != nil {
		r.mu.Unlock()
		return nil, nil, errors.New("forward routing is stopped")
	}
	if account != r.account {
		r.mu.Unlock()
		return nil, nil, errors.New("forward routing account mismatch")
	}
	r.active++
	r.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	done := func() {
		stop()
		cancel()
		r.mu.Lock()
		r.active--
		if r.closed && r.active == 0 {
			close(r.drained)
		}
		r.mu.Unlock()
	}
	return ctx, done, nil
}

func (r *MessageRouter) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
		if r.active == 0 {
			close(r.drained)
		}
	}
	r.mu.Unlock()
	select {
	case <-r.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func forwardSource(peer types.MessagePeer) (types.ChatRef, error) {
	if peer.ID <= 0 || (peer.Kind != forwardPeerUser && peer.Kind != "chat" && peer.Kind != forwardPeerChannel) {
		return "", errors.New("forward source requires a typed peer")
	}
	return types.ChatRef(fmt.Sprintf("%s:%d", peer.Kind, peer.ID)), nil
}

func (r *MessageRouter) Interested(ctx context.Context, account types.AccountID, peer types.MessagePeer) (bool, error) {
	ctx, done, err := r.begin(ctx, account)
	if err != nil {
		return false, err
	}
	defer done()
	source, err := forwardSource(peer)
	if err != nil {
		return false, err
	}
	destinations, _, _, err := r.destinations(ctx, source, true)
	return len(destinations) > 0, err
}

func (r *MessageRouter) destinations(ctx context.Context, source types.ChatRef, automatic bool) ([]types.ForwardDestination, bool, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, 0, err
	}
	if automatic && r.options.Rules != nil {
		if destinations := r.options.Rules.Destinations(ctx, source); len(destinations) > 0 {
			return destinations, false, 0, nil
		}
	}
	if automatic {
		matched, err := r.listens(ctx, source)
		if err != nil || !matched {
			return nil, true, 0, err
		}
	}
	if r.queue == nil {
		return nil, true, 0, errors.New("forward queue is unavailable")
	}
	settings := r.queue.policy()
	if settings.command.Mode != forwardModeDefault && settings.command.Mode != forwardModeClone {
		return nil, true, 0, fmt.Errorf("invalid forward mode %q", settings.command.Mode)
	}
	target, err := r.resolve(ctx, settings.command.Target, false)
	if err != nil {
		return nil, true, 0, fmt.Errorf("resolve default forward target: %w", err)
	}
	return []types.ForwardDestination{{Target: target.Reference, Name: target.Name, Mode: settings.command.Mode, Silent: settings.command.Silent}}, true, settings.dedupe, nil
}

func (r *MessageRouter) listens(ctx context.Context, source types.ChatRef) (bool, error) {
	if r.options.Listening == nil {
		return false, nil
	}
	settings, err := r.options.Listening.Listening(ctx, r.account)
	if err != nil {
		return false, err
	}
	var failures error
	for _, raw := range settings.Sources {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		peer, err := r.resolve(ctx, raw, settings.Comments)
		if peer.Reference == source || (settings.Comments && peer.LinkedDiscussion == source) {
			return true, nil
		}
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
	}
	return false, failures
}

func (r *MessageRouter) resolve(ctx context.Context, raw string, comments bool) (types.ForwardPeer, error) {
	if r.options.Peers == nil {
		return types.ForwardPeer{}, errors.New("forward peer resolver is unavailable")
	}
	r.peerMu.Lock()
	defer r.peerMu.Unlock()
	if err := ctx.Err(); err != nil {
		return types.ForwardPeer{}, err
	}
	key := fmt.Sprintf("%t:%s", comments, strings.TrimSpace(raw))
	now := time.Now()
	if cached, ok := r.peers[key]; ok && now.Before(cached.expires) {
		return cached.peer, cached.err
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	peer, err := r.options.Peers.ResolveForwardPeer(bounded, r.account, raw, comments)
	if ctx.Err() != nil {
		return types.ForwardPeer{}, ctx.Err()
	}
	if err == nil && peer.Reference == "" {
		err = errors.New("resolved forward peer has no typed reference")
	}
	ttl := 5 * time.Minute
	if err != nil {
		ttl = 5 * time.Second
	}
	for key, cached := range r.peers {
		if !now.Before(cached.expires) {
			delete(r.peers, key)
		}
	}
	r.peers[key] = peerLookup{peer: peer, err: err, expires: now.Add(ttl)}
	return peer, err
}

func (r *MessageRouter) SubmitMessage(ctx context.Context, message types.ForwardMessage) error {
	ctx, done, err := r.begin(ctx, message.Account)
	if err != nil {
		return err
	}
	defer done()
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.Outgoing {
		return nil
	}
	source, err := forwardSource(message.Peer)
	if err != nil {
		return err
	}
	if message.MessageID <= 0 || r.queue == nil {
		return errors.New("forward routing requires a message and queue")
	}
	destinations, defaultRoute, ttl, err := r.destinations(ctx, source, message.Automatic)
	if err != nil {
		return err
	}
	var result error
	for _, destination := range destinations {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if defaultRoute {
			result = errors.Join(result, r.enqueueDefault(ctx, message, destination, ttl))
		} else {
			_, err := r.queue.EnqueueRouted(ctx, message.Peer, message.MessageID, message.GroupedID, message.Origin, destination)
			result = errors.Join(result, err)
		}
	}
	return result
}

func (r *MessageRouter) enqueueDefault(ctx context.Context, message types.ForwardMessage, destination types.ForwardDestination, ttl time.Duration) error {
	identity := fmt.Sprintf("m:%d", message.MessageID)
	if message.GroupedID != 0 {
		identity = fmt.Sprintf("g:%d", message.GroupedID)
	}
	key := fmt.Sprintf("%s:%d/%s/%s/%s/%t", message.Peer.Kind, message.Peer.ID, identity, destination.Target, destination.Mode, destination.Silent)
	var claim *forwardClaim
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.claimMu.Lock()
		now := time.Now()
		for key, claim := range r.claims {
			if !claim.expires.IsZero() && !now.Before(claim.expires) {
				delete(r.claims, key)
			}
		}
		if existing, exists := r.claims[key]; exists {
			accepted := !existing.expires.IsZero()
			r.claimMu.Unlock()
			if accepted {
				return nil
			}
			// A concurrent attempt has not persisted yet. Wait for its result
			// before treating the message as accepted; retry if it failed.
			select {
			case <-existing.done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		claim = &forwardClaim{done: make(chan struct{})}
		r.claims[key] = claim
		r.claimMu.Unlock()
		break
	}
	_, err := r.queue.enqueueMessage(ctx, message.Peer, message.MessageID, message.Origin, string(destination.Target), destination.Name, destination.Mode, destination.Silent)
	r.claimMu.Lock()
	if err != nil {
		delete(r.claims, key)
	} else {
		claim.expires = time.Now().Add(ttl)
	}
	close(claim.done)
	r.claimMu.Unlock()
	return err
}
