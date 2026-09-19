package types

const DownloadRequested = "download.requested"

const ForwardRequested = "forward.requested"

type ForwardIntent struct {
	Automatic bool
	Account   AccountID
	Peer      MessagePeer
	PeerID    int64
	MessageID int
}

// MessagePeer carries the protocol reference without SDK objects or file data.
type MessagePeer struct {
	Kind       string
	ID         int64
	AccessHash int64
}

type DownloadIntent struct {
	RequestID string `json:"request_id,omitempty"`
	Account   AccountID
	Peer      MessagePeer
	PeerID    int64
	MessageID int
	Link      string
	Source    string
}

type DownloadSubmissionSummary struct {
	Link      string
	PeerID    int64
	MessageID int
	Total     int
	Queued    int
	Skipped   int
}
