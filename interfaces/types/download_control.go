package types

import "time"

type DownloadState string

const (
	DownloadQueued   DownloadState = "queued"
	DownloadActive   DownloadState = "active"
	DownloadPaused   DownloadState = "paused"
	DownloadComplete DownloadState = "complete"
	DownloadError    DownloadState = "error"
	DownloadRemoved  DownloadState = "removed"
	DownloadUnknown  DownloadState = "unknown"
)

type DownloadTask struct {
	Account        AccountID     `json:"account"`
	Executor       string        `json:"executor"`
	ID             string        `json:"id"`
	TaskID         string        `json:"task_id"`
	FileName       string        `json:"file_name"`
	Dir            string        `json:"dir"`
	Out            string        `json:"out"`
	Path           string        `json:"path"`
	Status         string        `json:"status"`
	State          DownloadState `json:"state"`
	Total          int64         `json:"total"`
	Completed      int64         `json:"completed"`
	Error          string        `json:"error,omitempty"`
	DownloadSpeed  int64         `json:"download_speed"`
	EtaSeconds     int64         `json:"eta_seconds"`
	ElapsedSeconds int64         `json:"elapsed_seconds"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type DownloadAction struct {
	Account  AccountID `json:"account"`
	Executor string    `json:"executor"`
	Action   string    `json:"action"`
	IDs      []string  `json:"ids"`
	Statuses []string  `json:"statuses"`
}

type DownloadActionResult struct {
	Matched int      `json:"matched"`
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}
