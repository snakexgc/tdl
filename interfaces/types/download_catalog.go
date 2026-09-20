package types

import "time"

type DownloadLinkItem struct {
	ID                 string           `json:"id"`
	Key                string           `json:"key"`
	URL                string           `json:"url"`
	FileName           string           `json:"file_name"`
	FileSize           int64            `json:"file_size"`
	PeerID             int64            `json:"peer_id"`
	MessageID          int              `json:"message_id"`
	CreatedAt          time.Time        `json:"created_at"`
	ExpiresAt          *time.Time       `json:"expires_at,omitempty"`
	Permanent          bool             `json:"permanent"`
	Expired            bool             `json:"expired"`
	Downloaded         bool             `json:"downloaded"`
	HTTPDownloaded     bool             `json:"http_downloaded"`
	HTTPDownloadedAt   *time.Time       `json:"http_downloaded_at,omitempty"`
	HTTPDeliveredBytes int64            `json:"http_delivered_bytes"`
	Status             string           `json:"status"`
	Aria2              []Aria2LinkEntry `json:"aria2"`
	Local              []LocalLinkEntry `json:"local"`
}

type Aria2LinkEntry struct {
	GID         string    `json:"gid"`
	Status      string    `json:"status"`
	Downloaded  bool      `json:"downloaded"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	CreatedAt   time.Time `json:"created_at"`
	Total       int64     `json:"total"`
	Completed   int64     `json:"completed"`
	Error       string    `json:"error,omitempty"`
}

type LocalLinkEntry struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Path      string    `json:"path"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PersistentLink struct {
	ID           string    `json:"id"`
	PeerID       int64     `json:"peer_id"`
	MessageID    int       `json:"message_id"`
	FileName     string    `json:"file_name"`
	FileSize     int64     `json:"file_size"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at,omitempty"`
	Downloaded   bool      `json:"downloaded"`
}

type LinkSubmissionResult struct {
	OK      bool     `json:"ok"`
	Added   int      `json:"added"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

type LinkCatalogRecord struct {
	Key                string
	Task               PersistentLink
	HTTPCompleted      bool
	HTTPCompletedAt    time.Time
	HTTPDeliveredBytes int64
	Aria2              []Aria2LinkEntry
	Local              []LocalLinkEntry
}

type LinkCatalogSnapshot struct {
	Records       []LinkCatalogRecord
	PublicBaseURL string
	TTL           time.Duration
	StatusError   string
}
