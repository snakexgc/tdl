package reaction

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TestReactionOwnershipAndDeduplication(t *testing.T) {
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {"download": []string{" fire "}, "forward": []string{}}})
	require.NoError(t, err)
	ctx := context.Background()
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	value, err := host.Resolve(ports.ReactionTriggerName)
	require.NoError(t, err)
	policy := value.(ports.ReactionTrigger)
	input := ports.ReactionInput{Account: types.DefaultAccount, Reactions: []ports.Reaction{{Value: "fire", Mine: true}}}
	require.True(t, policy.Matches(ctx, input))
	input.Partial = true
	require.False(t, policy.Matches(ctx, input))
	input.Partial = false
	input.Reactions[0].Mine = false
	require.False(t, policy.Matches(ctx, input))
	input.Reactions[0] = ports.Reaction{Value: "custom:123", Mine: true}
	require.False(t, policy.Matches(ctx, input))
	input.Forward = true
	require.True(t, policy.Matches(ctx, input), "empty trigger set accepts any owned reaction")
	input.Account = "other"
	require.False(t, policy.Matches(ctx, input))
	key := ports.ReactionKey{Account: types.DefaultAccount, PeerID: 12, MessageID: 34}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if policy.Claim(ctx, key) {
				claims.Add(1)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), claims.Load())
	policy.Forget(key)
	require.True(t, policy.Claim(ctx, key))
	require.NoError(t, host.Stop(ctx))
	policy.Forget(key)
	require.False(t, policy.Claim(ctx, key))
}
