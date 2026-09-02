package download

import "context"

// Submission describes a temporary HTTP download link and the optional target
// path requested by the watcher. Download backends consume this value without
// being part of the watch or HTTP server lifecycle.
type Submission struct {
	TaskID      string
	DownloadURL string
	Dir         string
	Out         string
	FullPath    string
}

// Result is the backend-specific identity returned after a submission.
type Result struct {
	Target string
	ID     string
}

// Submitter is an optional download backend. The watcher only creates HTTP
// tasks and links; runtime wiring decides whether a backend receives them.
type Submitter interface {
	Name() string
	Submit(ctx context.Context, submission Submission) (Result, error)
}
