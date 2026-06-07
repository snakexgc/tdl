package watch

import (
	"time"

	"github.com/go-faster/errors"
)

const (
	InternalDownloadStatusQueued   = "queued"
	InternalDownloadStatusActive   = "active"
	InternalDownloadStatusPaused   = "paused"
	InternalDownloadStatusComplete = "complete"
	InternalDownloadStatusError    = "error"
	InternalDownloadStatusRemoved  = "removed"
)

var (
	errInternalDownloadPaused  = errors.New("internal download paused")
	errInternalDownloadRemoved = errors.New("internal download removed")
)

type internalDownloadRecord struct {
	ID            string     `json:"id"`
	TaskID        string     `json:"task_id"`
	FileName      string     `json:"file_name"`
	Dir           string     `json:"dir"`
	Out           string     `json:"out"`
	Path          string     `json:"path"`
	Total         int64      `json:"total"`
	Completed     int64      `json:"completed"`
	Status        string     `json:"status"`
	Error         string     `json:"error,omitempty"`
	DownloadSpeed int64      `json:"download_speed,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// InternalDownloadInfo is the public view of a download record returned by the
// API. It extends the persisted record with computed fields (EtaSeconds,
// ElapsedSeconds) that are derived at read time and never stored.
type InternalDownloadInfo struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"task_id"`
	FileName       string     `json:"file_name"`
	Dir            string     `json:"dir"`
	Out            string     `json:"out"`
	Path           string     `json:"path"`
	Total          int64      `json:"total"`
	Completed      int64      `json:"completed"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	DownloadSpeed  int64      `json:"download_speed"`
	EtaSeconds     int64      `json:"eta_seconds"`
	ElapsedSeconds int64      `json:"elapsed_seconds"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// InternalDownloadOverview contains per-status counts for all tracked downloads.
type InternalDownloadOverview struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Queued   int `json:"queued"`
	Paused   int `json:"paused"`
	Complete int `json:"complete"`
	Error    int `json:"error"`
}

type InternalDownloadActionResult struct {
	Matched int      `json:"matched"`
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}
