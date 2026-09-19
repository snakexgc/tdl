package forwarder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

func TestRoutedAlbumReplayAndPeerKinds(t *testing.T) {
	ctx := context.Background()
	q := newTestQueue()
	source := types.MessagePeer{Kind: "channel", ID: 1}
	destination := types.ForwardDestination{RuleID: "one", Target: "chat:2", Mode: "default"}
	id, err := q.EnqueueRouted(ctx, source, 10, 42, "source", destination)
	require.NoError(t, err)
	destination.RuleID = "overlapping-rule"
	replayed, err := q.EnqueueRouted(ctx, source, 11, 42, "source", destination)
	require.NoError(t, err)
	require.Equal(t, id, replayed, "album and overlapping rules must not duplicate a target")
	destination.Target = "chat:3"
	_, err = q.EnqueueRouted(ctx, source, 11, 42, "source", destination)
	require.NoError(t, err)
	source.Kind = "user"
	_, err = q.EnqueueRouted(ctx, source, 11, 42, "source", destination)
	require.NoError(t, err)
	items, err := q.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 3)
	q2 := NewQueue(q.store)
	_, err = q2.EnqueueRouted(ctx, source, 12, 42, "source", destination)
	require.NoError(t, err)
	items, err = q2.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 3, "durable identity survives worker replacement")
}
