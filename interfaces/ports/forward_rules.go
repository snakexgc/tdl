package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const ForwardRulesName = "forward.rules"

type ForwardRules interface {
	Destinations(context.Context, types.ChatRef) []types.ForwardDestination
}

type DialogCatalog interface {
	Dialogs(context.Context) ([]types.Dialog, error)
}
