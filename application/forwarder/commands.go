package forwarder

import "github.com/snakexgc/tdl/interfaces/types"

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: "forward", Description: "回复 Telegram 消息链接并转发", Owner: ID},
	}
}
