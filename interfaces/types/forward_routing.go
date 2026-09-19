package types

import "time"

type ForwardListening struct {
	Sources  []string
	Comments bool
}

type ForwardDefaults struct {
	Target, Mode string
	Silent       bool
	DedupeTTL    time.Duration
}

type ForwardPeer struct {
	Reference        ChatRef
	Name             string
	LinkedDiscussion ChatRef
}

type ForwardMessage struct {
	Account             AccountID
	Peer                MessagePeer
	MessageID           int
	GroupedID           int64
	Origin              string
	Automatic, Outgoing bool
}
