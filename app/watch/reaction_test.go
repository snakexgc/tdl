package watch

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestIsMyMessageReactionsRequiresExplicitCurrentUser(t *testing.T) {
	w := &Watcher{}
	ctx := context.Background()

	require.False(t, w.isMyMessageReactions(ctx, &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCount(false)},
	}, 100, 1))

	require.False(t, w.isMyMessageReactions(ctx, &tg.MessageReactions{
		Min:     true,
		Results: []tg.ReactionCount{testReactionCount(true)},
	}, 100, 1))

	require.True(t, w.isMyMessageReactions(ctx, &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCount(true)},
	}, 100, 1))

	reactions := tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCount(false)},
	}
	reactions.SetRecentReactions([]tg.MessagePeerReaction{{
		My:       true,
		PeerID:   &tg.PeerUser{UserID: 42},
		Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
	}})
	require.True(t, w.isMyMessageReactions(ctx, &reactions, 100, 1))
}

func TestEditMessageReactionSkipsWhenNotMine(t *testing.T) {
	w := &Watcher{jobCh: make(chan downloadJob, 1)}
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(false)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Empty(t, w.jobCh)
}

func TestEditMessageReactionQueuesWhenMine(t *testing.T) {
	w := &Watcher{jobCh: make(chan downloadJob, 1)}
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(true)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Len(t, w.jobCh, 1)
}

func testReactionCount(chosen bool) tg.ReactionCount {
	count := tg.ReactionCount{
		Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
		Count:    1,
	}
	if chosen {
		count.SetChosenOrder(1)
	}
	return count
}
