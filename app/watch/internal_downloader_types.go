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
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	FileName  string    `json:"file_name"`
	Dir       string    `json:"dir"`
	Out       string    `json:"out"`
	Path      string    `json:"path"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type InternalDownloadInfo struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	FileName  string    `json:"file_name"`
	Dir       string    `json:"dir"`
	Out       string    `json:"out"`
	Path      string    `json:"path"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type InternalDownloadActionResult struct {
	Matched int      `json:"matched"`
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}
