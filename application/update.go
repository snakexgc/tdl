package application

import (
	updater "github.com/snakexgc/tdl/application/update.self"
	"github.com/snakexgc/tdl/interfaces/types"
)

// These process entry points run before the component runtime is available.
func StartUpdateApply(plan types.UpdatePlan, target string, args []string) error {
	return updater.StartApply(plan, target, args)
}
func RunUpdateApply(args []string) error { return updater.RunApply(args) }
func StartAttached(path string, args []string, cwd string) error {
	return updater.StartAttached(path, args, cwd)
}
