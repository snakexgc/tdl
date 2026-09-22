package consolebot

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

var commandPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Dispatcher struct {
	policy    ports.Console
	commands  []types.ConsoleCommand
	handlers  map[string]ports.ConsoleCommandHandler
	canonical map[string]string
}

func NewDispatcher(policy ports.Console, contributions []ports.ConsoleContribution) (*Dispatcher, error) {
	d := &Dispatcher{policy: policy, handlers: map[string]ports.ConsoleCommandHandler{}, canonical: map[string]string{}}
	for _, contribution := range contributions {
		if contribution.Handler == nil {
			return nil, fmt.Errorf("command handler is required")
		}
		for _, command := range contribution.Commands {
			for _, name := range append([]string{command.Name}, command.Aliases...) {
				if !commandPattern.MatchString(name) {
					return nil, fmt.Errorf("invalid command name %q", name)
				}
				if _, exists := d.handlers[name]; exists {
					return nil, fmt.Errorf("duplicate command %s", name)
				}
				d.handlers[name] = contribution.Handler
				d.canonical[name] = command.Name
			}
			d.commands = append(d.commands, command)
		}
	}
	return d, nil
}

func (d *Dispatcher) Commands() []types.ConsoleCommand {
	commands := cloneCommands(d.commands)
	sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands
}
func (d *Dispatcher) PrivateCommand(name string) bool { _, exists := d.handlers[name]; return exists }
func (d *Dispatcher) Allowed(account types.AccountID, user int64) bool {
	return d.policy != nil && d.policy.Allowed(account, user)
}

func (d *Dispatcher) Dispatch(ctx context.Context, request types.ConsoleRequest) (bool, types.ConsoleResponse, error) {
	handler, exists := d.handlers[request.Name]
	if !exists {
		return false, types.ConsoleResponse{}, nil
	}
	if !d.Allowed(request.Account, request.UserID) {
		return true, types.ConsoleResponse{}, fmt.Errorf("console access denied")
	}
	if !request.Private {
		return true, types.ConsoleResponse{Text: "请在私聊中发送控制命令。"}, nil
	}
	if err := ctx.Err(); err != nil {
		return true, types.ConsoleResponse{}, err
	}
	request.Name = d.canonical[request.Name]
	response, err := handler.Execute(ctx, request)
	return true, response, err
}
