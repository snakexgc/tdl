package types

const DownloadRequested = "download.requested"

const DownloadSourceBotForward = "bot_forward"

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
	Forwarded *ForwardedMedia
}

// ForwardedMedia carries the Bot API file reference and metadata for a
// forwarded message. The file bytes remain on the Telegram data path.
type ForwardedMedia struct {
	FileID   string
	FileName string
	FileSize int64
	MIMEType string
	Caption  string
	Date     int64
	AlbumID  string
	PeerName string
}

type DownloadSubmissionSummary struct {
	Link      string
	PeerID    int64
	MessageID int
	Total     int
	Queued    int
	Skipped   int
	Failed    int
	Uncertain int
	Links     []GeneratedDownloadLink
}

type GeneratedDownloadLink struct {
	FileName string
	URL      string
}
