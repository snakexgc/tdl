package forwarder

import "github.com/snakexgc/tdl/interfaces/types"

const (
	commandPort        = "forwarder.command"
	forwardCommandName = "forward"
	forwardModeDefault = "default"
)

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: forwardCommandName, Description: "回复 Telegram 消息链接并转发", Owner: ID, Port: commandPort},
	}
}
