package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const UpdaterName = "update.self"

type Updater interface {
	Check(context.Context) (types.UpdateInfo, error)
	Download(context.Context) (types.UpdatePlan, types.UpdateInfo, error)
}
