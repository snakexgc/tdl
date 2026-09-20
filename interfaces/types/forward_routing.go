package types

type ForwardListening struct {
	Sources  []string
	Comments bool
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
