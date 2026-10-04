package types

import "time"

const (
	DownloadSourceListen        = "listen"
	DownloadSourceListenComment = "listen_comment"
	ListenRecordVersion         = 1
)

type DownloadListenRule struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Enabled        bool      `json:"enabled"`
	Sources        []ChatRef `json:"sources"`
	ListenComments bool      `json:"listen_comments"`
}

// ListenMessage is update metadata; SDK objects and file bytes stay in the adapter.
type ListenMessage struct {
	Peer        MessagePeer `json:"peer"`
	ID          int         `json:"id"`
	Date        int64       `json:"date"`
	Outgoing    bool        `json:"outgoing,omitempty"`
	Broadcast   bool        `json:"broadcast,omitempty"`
	MediaID     string      `json:"media_id,omitempty"`
	FileName    string      `json:"file_name,omitempty"`
	FileSize    int64       `json:"file_size,omitempty"`
	Video       bool        `json:"video,omitempty"`
	Pending     bool        `json:"pending,omitempty"`
	ReplyTo     int         `json:"reply_to,omitempty"`
	ThreadID    int         `json:"thread_id,omitempty"`
	ForwardPeer MessagePeer `json:"forward_peer,omitempty"`
	ForwardID   int         `json:"forward_id,omitempty"`
	ForwardDate int64       `json:"forward_date,omitempty"`
}

type ListenPeer struct {
	Peer       MessagePeer `json:"peer"`
	Title      string      `json:"title"`
	Broadcast  bool        `json:"broadcast"`
	Discussion MessagePeer `json:"discussion"`
}

type ListenPage struct {
	Messages   []ListenMessage
	NextOffset int
	Done       bool
}

type ListenPolicyRecord struct {
	Signature string            `json:"signature"`
	Starts    map[ChatRef]int64 `json:"starts"`
}

type ListenSourceRecord struct {
	Version           int        `json:"version"`
	Ref               ChatRef    `json:"ref"`
	ActivatedAt       int64      `json:"activated_at"`
	Info              ListenPeer `json:"info"`
	Cursor            int        `json:"cursor"`
	LastMessageAt     int64      `json:"last_message_at"`
	LastScanAt        time.Time  `json:"last_scan_at"`
	LastCommentScanAt time.Time  `json:"last_comment_scan_at"`
	NextScanAt        time.Time  `json:"next_scan_at"`
	Queued            int        `json:"queued"`
	Filtered          int        `json:"filtered"`
	Failed            int        `json:"failed"`
	LastError         string     `json:"last_error"`
}

type ListenThreadRecord struct {
	Version    int         `json:"version"`
	ID         string      `json:"id"`
	Source     ChatRef     `json:"source"`
	PostID     int         `json:"post_id"`
	PostDate   int64       `json:"post_date"`
	Peer       MessagePeer `json:"peer"`
	RootID     int         `json:"root_id"`
	Cursor     int         `json:"cursor"`
	ExpiresAt  time.Time   `json:"expires_at"`
	NextScanAt time.Time   `json:"next_scan_at"`
	LastScanAt time.Time   `json:"last_scan_at"`
	LastError  string      `json:"last_error"`
}

type ListenJobRecord struct {
	Version       int           `json:"version"`
	ID            string        `json:"id"`
	Message       ListenMessage `json:"message"`
	Source        ChatRef       `json:"source"`
	Comment       bool          `json:"comment"`
	PostDate      int64         `json:"post_date,omitempty"`
	State         string        `json:"state"`
	Attempts      int           `json:"attempts"`
	NextAttemptAt time.Time     `json:"next_attempt_at"`
	LastError     string        `json:"last_error"`
}

type ListenState struct {
	Policy        ListenPolicyRecord
	PendingPolicy ListenPolicyRecord
	Sources       map[ChatRef]ListenSourceRecord
	Threads       map[string]ListenThreadRecord
	Jobs          map[string]ListenJobRecord
}

type ListenMutation struct {
	Policy        *ListenPolicyRecord
	PendingPolicy *ListenPolicyRecord
	Sources       []ListenSourceRecord
	Threads       []ListenThreadRecord
	DeleteThreads []string
	Jobs          []ListenJobRecord
}

type ListenStatus struct {
	Connected           bool                 `json:"connected"`
	DownloadEnabled     bool                 `json:"download_enabled"`
	ScanIntervalSeconds int                  `json:"scan_interval_seconds"`
	Sources             []ListenSourceRecord `json:"sources"`
	Tasks               []ListenTask         `json:"tasks"`
	Pending             int                  `json:"pending"`
	Uncertain           int                  `json:"uncertain"`
	Tracking            int                  `json:"tracking"`
	LastError           string               `json:"last_error,omitempty"`
	Filters             ListenFilters        `json:"filters"`
}

// ListenTask projects admission metadata only. The UI joins TaskID with the
// shared download-control snapshot for current transfer status and progress.
type ListenTask struct {
	ID        string  `json:"id"`
	Source    ChatRef `json:"source"`
	TaskID    string  `json:"task_id"`
	MessageID int     `json:"message_id"`
	Date      int64   `json:"date"`
	FileName  string  `json:"file_name"`
	Total     int64   `json:"total"`
	Comment   bool    `json:"comment"`
	State     string  `json:"state"`
	Error     string  `json:"error,omitempty"`
}

type ListenFilters struct {
	DownloadEnabled bool     `json:"-"`
	Include         []string `json:"include"`
	Exclude         []string `json:"exclude"`
	MinMB           int64    `json:"min_mb"`
	MaxMB           int64    `json:"max_mb"`
	Limit           int      `json:"limit"`
	DCConnections   int      `json:"dc_connections"`
}
