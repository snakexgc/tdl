package types

import "time"

// Job lifecycle statuses.
const (
	StatusQueued   = "queued"   // eligible to run now
	StatusRunning  = "running"  // currently being forwarded (at most one)
	StatusPaused   = "paused"   // suspended by the user
	StatusRetrying = "retrying" // failed transiently, waiting for backoff
	StatusDone     = "done"     // completed
	StatusError    = "error"    // failed permanently (resume to retry manually)
)

// Job trigger source.
const (
	SourceCommand = "command" // /forward bot command
	SourceWatch   = "watch"   // watcher auto-forward
)

// Job is both the persisted record and the WebUI payload for a forward task.
type ForwardJob struct {
	ID     string `json:"id"`
	Source string `json:"source"`

	// Exactly one source form is set: a raw message link (bot command) or a
	// resolved peer+message id (watcher), so the worker can re-fetch it.
	SourceLink      string `json:"source_link,omitempty"`
	SourcePeerID    int64  `json:"source_peer_id,omitempty"`
	SourcePeerKind  string `json:"source_peer_kind,omitempty"`
	RuleID          string `json:"rule_id,omitempty"`
	SourceMessageID int    `json:"source_message_id,omitempty"`
	OriginName      string `json:"origin_name,omitempty"`

	Destination     string `json:"destination"` // target string ("" = Saved Messages)
	DestinationName string `json:"destination_name,omitempty"`

	Mode   string `json:"mode"` // config mode name: "default" (direct) or "clone"
	Silent bool   `json:"silent,omitempty"`

	Status     string `json:"status"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	CloneDone  int64  `json:"clone_done,omitempty"`
	CloneTotal int64  `json:"clone_total,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	Error      string `json:"error,omitempty"`

	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

func (j ForwardJob) Terminal() bool {
	return j.Status == StatusDone || j.Status == StatusError
}

// ActionResult mirrors watch.InternalDownloadActionResult so the WebUI can reuse
// the same bulk-action handling.
type ForwardActionResult struct {
	Matched int      `json:"matched"`
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}
