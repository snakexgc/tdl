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

func TestCommandTextPreservesPayloadAndRemovesBotSuffix(t *testing.T) {
	for _, input := range []string{"/downloads_active", "/downloads_active@mybot", " /downloads_active  two words\nnext "} {
		request := types.ConsoleRequest{Name: "downloads_active", Text: input}
		expected := "/downloads_active"
		if input[0] == ' ' {
			expected += "  two words\nnext"
		}
		require.Equal(t, expected, canonicalCommandText(request))
	}
}

type factoryConsole struct {
	ports.Console
	enabled bool
}

func (p *factoryConsole) PrivateCommand(string) bool { return p.enabled }

type factoryTasks struct {
	ports.Aria2Tasks
	calls int
}

func (p *factoryTasks) PauseTask(context.Context, string) error { p.calls++; return nil }

func TestAria2FactoryHonorsStoppedComponentsAndReenable(t *testing.T) {
	ctx := context.Background()
	console := &factoryConsole{enabled: true}
	first, second := &factoryTasks{}, &factoryTasks{}
	current := first
	controlEnabled, executorEnabled := true, true
	factory := componentAria2Factory(console, func(owner, port string) (any, error) {
		switch port {
		case ports.DownloadControlName:
			if !controlEnabled {
				return nil, fmt.Errorf("control disabled")
			}
			return &downloadControlSpy{}, nil
		case ports.Aria2TasksName:
			if !executorEnabled {
				return nil, fmt.Errorf("executor disabled")
			}
			return current, nil
		default:
			return nil, fmt.Errorf("unknown port %s/%s", owner, port)
		}
	})
	require.NoError(t, factory().PauseTask(ctx, downloadTestTask))
	console.enabled = false
	require.Error(t, factory().PauseTask(ctx, downloadTestTask))
	console.enabled, controlEnabled = true, false
	require.Error(t, factory().PauseTask(ctx, downloadTestTask))
	controlEnabled, executorEnabled = true, false
	require.Error(t, factory().PauseTask(ctx, downloadTestTask))
	executorEnabled, current = true, second
	require.NoError(t, factory().PauseTask(ctx, downloadTestTask))
	require.Equal(t, 1, first.calls)
	require.Equal(t, 1, second.calls)
	require.Error(t, componentAria2Factory(console, nil)().PauseTask(ctx, downloadTestTask))
}
