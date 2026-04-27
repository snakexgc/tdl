package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"go.etcd.io/bbolt"

	"github.com/iyear/tdl/app/bot"
	"github.com/iyear/tdl/app/updater"
	"github.com/iyear/tdl/cmd"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__apply-update" {
		if err := updater.RunApply(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	humanizeErrors := map[error]string{
		bbolt.ErrTimeout: "Current database is used by another process, please terminate it first",
	}

	if err := cmd.New().ExecuteContext(ctx); err != nil {
		for e, m := range humanizeErrors {
			if errors.Is(err, e) {
				color.Red("%s", m)
				os.Exit(1)
			}
		}

		color.Red("Error: %+v", err)
		os.Exit(1)
	}
	if plan, ok := bot.UpdateRequested(); ok {
		if err := startUpdate(plan); err != nil {
			color.Red("Update failed: %+v", err)
			os.Exit(1)
		}
		return
	}
	if bot.RebootRequested() {
		if err := restartCurrentProcess(); err != nil {
			color.Red("Restart failed: %+v", err)
			os.Exit(1)
		}
		color.Green("Restarted.")
	}
}

func restartCurrentProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "get executable path")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errors.Wrap(err, "get working directory")
	}

	return updater.StartAttached(exe, os.Args[1:], cwd)
}

func startUpdate(plan updater.Plan) error {
	exe, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "get executable path")
	}
	return updater.StartApply(plan, exe, os.Args[1:])
}
