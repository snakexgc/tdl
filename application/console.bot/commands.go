package consolebot

import "github.com/snakexgc/tdl/interfaces/types"

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: "reboot", Description: "重启(不推荐)", Owner: ID},
	}
}
