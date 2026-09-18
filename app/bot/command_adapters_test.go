package bot

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func TestDeclaredCommandsResolveCurrentInstanceForEveryRequest(t *testing.T) {
	enabled, generation := true, "first"
	commands := []types.ConsoleCommand{{Owner: "example.feature", Port: "example.command", Name: "example"}}
	contributions := declaredCommandHandlers(commands, func(owner, port string) (any, error) {
		require.Equal(t, "example.feature", owner)
		require.Equal(t, "example.command", port)
		if !enabled {
			return nil, fmt.Errorf("disabled")
		}
		value := generation
		return ports.ConsoleCommandFunc(func(context.Context, types.ConsoleRequest) (types.ConsoleResponse, error) {
			return types.ConsoleResponse{Text: value}, nil
		}), nil
	})
	require.Len(t, contributions, 1)
	response, err := contributions[0].Handler.Execute(context.Background(), types.ConsoleRequest{})
	require.NoError(t, err)
	require.Equal(t, "first", response.Text)
	generation = "replacement"
	response, err = contributions[0].Handler.Execute(context.Background(), types.ConsoleRequest{})
	require.NoError(t, err)
	require.Equal(t, "replacement", response.Text)
	enabled = false
	_, err = contributions[0].Handler.Execute(context.Background(), types.ConsoleRequest{})
	require.ErrorContains(t, err, "disabled")
}
