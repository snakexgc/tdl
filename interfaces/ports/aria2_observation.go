package ports

import (
	"context"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

// ObservedLink carries an opaque version; callers never receive protocol media
// or storage handles. The repository checks it again when applying observations.
type ObservedLink struct {
	Task    types.PersistentLink
	Version string
}

type Aria2ObservationSnapshot struct {
	Links   map[string]ObservedLink
	Records map[string]types.Aria2TaskRecord
}

type Aria2Observations interface {
	Snapshot(context.Context) (Aria2ObservationSnapshot, error)
	Apply(context.Context, ObservedLink, types.Aria2TaskRecord, bool, time.Time, time.Duration) (bool, error)
	Cleanup(context.Context, time.Time, time.Duration) error
}
