package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

// WebAction is a component-owned JSON control action. The panel supplies auth
// and HTTP parsing; the component owns validation and business decisions.
type WebAction interface {
	Handle(context.Context, types.WebRequest) (types.WebResponse, error)
}
