package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const ReactionTriggerName = "trigger.reactions"

type Reaction struct {
	Value string
	Mine  bool
}

type ReactionInput struct {
	Account   types.AccountID
	Partial   bool
	Reactions []Reaction
	Forward   bool
}

type ReactionKey struct {
	Account   types.AccountID
	PeerID    int64
	MessageID int
}

type ReactionTrigger interface {
	Matches(context.Context, ReactionInput) bool
	Claim(context.Context, ReactionKey) bool
	Forget(ReactionKey)
}
