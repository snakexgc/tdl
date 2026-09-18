package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const KVMaintenanceName = "storage.maintenance"

type CleanupResult struct {
	Namespace     string
	Deleted, Kept int
	Errors        []string
}
type CleanupSnapshot struct {
	Records   map[string][]byte
	Protected int
}
type CleanupRepository interface {
	Snapshot(context.Context) (CleanupSnapshot, error)
	DeleteUnchanged(context.Context, string, []byte) (bool, error)
}
type KVMaintenance interface {
	Clean(context.Context, types.AccountID) (CleanupResult, error)
}
