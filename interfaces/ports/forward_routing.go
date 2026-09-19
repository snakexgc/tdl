package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	ForwardListeningName = "forward.listening"
	ForwardRoutingName   = "forward.routing"
)

type ForwardListening interface {
	Listening(context.Context, types.AccountID) (types.ForwardListening, error)
}

// ForwardPeers resolves protocol metadata; it does not select destinations.
type ForwardPeers interface {
	ResolveForwardPeer(context.Context, types.AccountID, string, bool) (types.ForwardPeer, error)
}

type ForwardRouting interface {
	Interested(context.Context, types.AccountID, types.MessagePeer) (bool, error)
	SubmitMessage(context.Context, types.ForwardMessage) error
}
