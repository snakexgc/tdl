package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

type ConsoleCommandHandler interface {
	Execute(context.Context, types.ConsoleRequest) (types.ConsoleResponse, error)
}
type ConsoleCommandFunc func(context.Context, types.ConsoleRequest) (types.ConsoleResponse, error)

func (f ConsoleCommandFunc) Execute(ctx context.Context, request types.ConsoleRequest) (types.ConsoleResponse, error) {
	return f(ctx, request)
}

type ConsoleContribution struct {
	Commands []types.ConsoleCommand
	Handler  ConsoleCommandHandler
}
