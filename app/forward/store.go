package forward

import (
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type (
	Job          = types.ForwardJob
	ActionResult = types.ForwardActionResult
)

const (
	StatusQueued   = types.StatusQueued
	StatusRunning  = types.StatusRunning
	StatusPaused   = types.StatusPaused
	StatusRetrying = types.StatusRetrying
	StatusDone     = types.StatusDone
	StatusError    = types.StatusError
	SourceCommand  = types.SourceCommand
	SourceWatch    = types.SourceWatch
)

func newJobStore(kv storage.Storage) *taskhub.ForwardRepository {
	return taskhub.NewForwardRepository(kv)
}
