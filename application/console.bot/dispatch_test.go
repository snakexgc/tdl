package consolebot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type examplePolicy struct{ ports.Console }

func (examplePolicy) Allowed(account types.AccountID, user int64) bool {
	return account == types.DefaultAccount && user == 1
}

func TestContributedCommandSharesMenuPermissionAliasAndDispatch(t *testing.T) {
	calls := 0
	contribution := ports.ConsoleContribution{Commands: []types.ConsoleCommand{{Owner: "example", Name: "example", Description: "Example component", Aliases: []string{"example_alias"}}}, Handler: ports.ConsoleCommandFunc(func(_ context.Context, request types.ConsoleRequest) (types.ConsoleResponse, error) {
		calls++
		return types.ConsoleResponse{Text: request.Text}, nil
	})}
	dispatcher, err := NewDispatcher(examplePolicy{}, []ports.ConsoleContribution{contribution})
	require.NoError(t, err)
	require.Equal(t, "example", dispatcher.Commands()[0].Name)
	request := types.ConsoleRequest{Account: types.DefaultAccount, UserID: 1, Name: "example_alias", Text: "unchanged input", Private: true}
	handled, response, err := dispatcher.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, request.Text, response.Text)
	request.UserID = 2
	_, _, err = dispatcher.Dispatch(context.Background(), request)
	require.ErrorContains(t, err, "denied")
	request.UserID = 1
	request.Private = false
	_, response, err = dispatcher.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.NotEmpty(t, response.Text)
	require.Equal(t, 1, calls)
	_, err = NewDispatcher(examplePolicy{}, []ports.ConsoleContribution{contribution, contribution})
	require.ErrorContains(t, err, "duplicate")
	disabled, err := NewDispatcher(examplePolicy{}, nil)
	require.NoError(t, err)
	require.Empty(t, disabled.Commands())
	handled, _, err = disabled.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.False(t, handled)
}
