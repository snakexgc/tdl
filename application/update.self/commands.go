package updater

import "github.com/snakexgc/tdl/interfaces/types"

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: "update_tdl", Description: "检查并更新 tdl", Owner: ID},
	}
}
