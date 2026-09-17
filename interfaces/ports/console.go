package ports

import "github.com/snakexgc/tdl/interfaces/types"

const ConsoleName = "console.bot"

type Console interface {
	Allowed(types.AccountID, int64) bool
	Commands() []types.ConsoleCommand
	PrivateCommand(string) bool
}
