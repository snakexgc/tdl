package watch

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestIsMyMessageReactionsRequiresExplicitCurrentUser(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{})
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

func TestIsMyMessageReactionsRequiresConfiguredTrigger(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{opts: Options{Reaction: testReactionPolicy(t, []string{"🔥"}, nil)}})
	ctx := context.Background()

	require.False(t, w.isMyMessageReactions(ctx, &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("👍", true)},
	}, 100, 1))

	require.True(t, w.isMyMessageReactions(ctx, &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("🔥", true)},
	}, 100, 1))
}

func TestIsMyRecentMessageReactionRequiresConfiguredTrigger(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{opts: Options{Reaction: testReactionPolicy(t, []string{"🔥"}, nil)}})
	ctx := context.Background()

	reactions := tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCount(false)},
	}
	reactions.SetRecentReactions([]tg.MessagePeerReaction{{
		My:       true,
		PeerID:   &tg.PeerUser{UserID: 42},
		Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
	}})
	require.False(t, w.isMyMessageReactions(ctx, &reactions, 100, 1))

	reactions.SetRecentReactions([]tg.MessagePeerReaction{{
		My:       true,
		PeerID:   &tg.PeerUser{UserID: 42},
		Reaction: &tg.ReactionEmoji{Emoticon: "🔥"},
	}})
	require.True(t, w.isMyMessageReactions(ctx, &reactions, 100, 1))
}

func TestEditMessageReactionSkipsWhenNotMine(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{intents: &intentRecorder{}})
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(false)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Empty(t, w.intents.(*intentRecorder).requests)
}

func TestEditMessageReactionQueuesWhenMine(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{intents: &intentRecorder{}, opts: Options{Download: true}})
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(true)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Len(t, w.intents.(*intentRecorder).requests, 1)
}

func TestEditMessageReactionSkipsWhenTriggerNotConfigured(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{
		intents: &intentRecorder{},
		opts:    Options{Reaction: testReactionPolicy(t, []string{"🔥"}, nil)},
	})
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(true)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Empty(t, w.intents.(*intentRecorder).requests)
}

func TestEditMessageReactionSkipsQueueWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := reactionTestWatcher(t, &Watcher{intents: &intentRecorder{}})
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(true)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(ctx, tg.Entities{}, msg))
	require.Empty(t, w.intents.(*intentRecorder).requests)
}

func TestEditMessageReactionRemovalClearsDedup(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{intents: &intentRecorder{}, opts: Options{Download: true}})
	msg := &tg.Message{
		ID:     116103,
		PeerID: &tg.PeerChannel{ChannelID: 2578606138},
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{testReactionCount(true)},
		},
	}

	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Len(t, w.intents.(*intentRecorder).requests, 1)

	msg.Reactions = tg.MessageReactions{}
	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))

	msg.Reactions.Results = []tg.ReactionCount{testReactionCount(true)}
	require.NoError(t, w.onEditMessageReaction(context.Background(), tg.Entities{}, msg))
	require.Len(t, w.intents.(*intentRecorder).requests, 2)
}

func TestShouldTriggerForwardReactionIgnoresListenSet(t *testing.T) {
	// Empty listen set with a configured trigger reaction is a valid
	// "react to forward" setup: reacting must still trigger a forward.
	w := reactionTestWatcher(t, &Watcher{opts: Options{Forward: true, Reaction: testReactionPolicy(t, nil, []string{"🔥"})}})

	myTrigger := &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("🔥", true)},
	}
	require.True(t, w.shouldTriggerForwardReaction(context.Background(), myTrigger))

	// A reaction that is not the configured trigger must not forward.
	myOther := &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("👍", true)},
	}
	require.False(t, w.shouldTriggerForwardReaction(context.Background(), myOther))

	// Someone else's trigger reaction must not forward.
	notMine := &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("🔥", false)},
	}
	require.False(t, w.shouldTriggerForwardReaction(context.Background(), notMine))
}

func TestShouldTriggerForwardReactionRequiresEnabledForward(t *testing.T) {
	reactions := &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("🔥", true)},
	}

	// No forward runtime configured.
	require.False(t, (reactionTestWatcher(t, &Watcher{})).shouldTriggerForwardReaction(context.Background(), reactions))

	// Forward configured but not enabled.
	disabled := reactionTestWatcher(t, &Watcher{opts: Options{Reaction: testReactionPolicy(t, nil, []string{"🔥"})}})
	require.False(t, disabled.shouldTriggerForwardReaction(context.Background(), reactions))
}

func TestShouldTriggerForwardReactionEmptyTriggerMatchesAnyEmoji(t *testing.T) {
	// An empty forward trigger set means any of the current user's reactions
	// forwards, mirroring the download trigger behaviour.
	w := reactionTestWatcher(t, &Watcher{opts: Options{Forward: true}})

	require.True(t, w.shouldTriggerForwardReaction(context.Background(), &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("🔥", true)},
	}))
	require.True(t, w.shouldTriggerForwardReaction(context.Background(), &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("👍", true)},
	}))
	// Still must be the current user's reaction, not someone else's.
	require.False(t, w.shouldTriggerForwardReaction(context.Background(), &tg.MessageReactions{
		Results: []tg.ReactionCount{testReactionCountWithEmoji("🔥", false)},
	}))
}

func TestGenerateMessageLinkForPrivateChatUsesTelegramDeepLink(t *testing.T) {
	w := reactionTestWatcher(t, &Watcher{})

	link := w.generateMessageLink(&tg.PeerUser{UserID: 8789880052}, 2247)

	require.Equal(t, "tg://openmessage?user_id=8789880052&message_id=2247", link)
}

func testReactionCount(chosen bool) tg.ReactionCount {
	return testReactionCountWithEmoji("👍", chosen)
}

func testReactionCountWithEmoji(emoji string, chosen bool) tg.ReactionCount {
	count := tg.ReactionCount{
		Reaction: &tg.ReactionEmoji{Emoticon: emoji},
		Count:    1,
	}
	if chosen {
		count.SetChosenOrder(1)
	}
	return count
}
