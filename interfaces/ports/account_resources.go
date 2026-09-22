package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const AccountResourcesName = "account.resources"

// AccountResources exposes lifecycle operations without exposing SDK clients,
// auth keys or session bytes to application components.
type AccountResources interface {
	Drain(context.Context, types.AccountID) error
	Stop(context.Context) error
}
