package consolebot

import "github.com/snakexgc/tdl/interfaces/types"

func cloneCommands(commands []types.ConsoleCommand) []types.ConsoleCommand {
	result := append([]types.ConsoleCommand{}, commands...)
	for i := range result {
		result[i].Aliases = append([]string{}, result[i].Aliases...)
	}
	return result
}
