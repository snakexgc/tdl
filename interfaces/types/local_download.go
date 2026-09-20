package types

import (
	"time"

	"github.com/go-faster/errors"
)

const (
	LocalDownloadStatusQueued   = "queued"
	LocalDownloadStatusActive   = "active"
	LocalDownloadStatusPaused   = "paused"
	LocalDownloadStatusComplete = "complete"
	LocalDownloadStatusError    = "error"
	LocalDownloadStatusRemoved  = "removed"
)

var (
	ErrLocalDownloadPaused  = errors.New("local download paused")
	ErrLocalDownloadRemoved = errors.New("local download removed")
)

type LocalDownloadRecord struct {
	State         DownloadState `json:"state"`
	Revision      uint64        `json:"revision"`
	ID            string        `json:"id"`
	TaskID        string        `json:"task_id"`
	FileName      string        `json:"file_name"`
	Dir           string        `json:"dir"`
	Out           string        `json:"out"`
	Path          string        `json:"path"`
	Total         int64         `json:"total"`
	Completed     int64         `json:"completed"`
	Status        string        `json:"status"`
	Error         string        `json:"error,omitempty"`
	DownloadSpeed int64         `json:"download_speed,omitempty"`
	StartedAt     *time.Time    `json:"started_at,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// LocalDownloadInfo is the public view of a download record returned by the
// API. It extends the persisted record with computed fields (EtaSeconds,
// ElapsedSeconds) that are derived at read time and never stored.
type LocalDownloadInfo struct {
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

// LocalDownloadOverview contains per-status counts for all tracked downloads.
type LocalDownloadOverview struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Queued   int `json:"queued"`
	Paused   int `json:"paused"`
	Complete int `json:"complete"`
	Error    int `json:"error"`
}

type LocalDownloadActionResult struct {
	Matched int      `json:"matched"`
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

type LocalDownloadSource struct {
	ID        string
	FileName  string
	FileSize  int64
	DC        int
	Available bool
}
