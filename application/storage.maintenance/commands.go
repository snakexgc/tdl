package maintenance

import "github.com/snakexgc/tdl/interfaces/types"

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: "clean_kv", Description: "清空KV缓存(危险)", Owner: ID},
	}
}
