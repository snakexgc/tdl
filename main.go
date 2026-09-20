package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	bberrors "go.etcd.io/bbolt/errors"

	"github.com/snakexgc/tdl/app/bot"
	"github.com/snakexgc/tdl/app/reset"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/cmd"
	"github.com/snakexgc/tdl/interfaces/types"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__apply-update" {
		if err := application.RunUpdateApply(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	humanizeErrors := map[error]string{
		bberrors.ErrTimeout: "Current database is used by another process, please terminate it first",
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
	if plan := reset.Requested(); plan != nil {
		if err := plan.Execute(); err != nil {
			color.Red("Reset failed; TDL remains stopped: %+v", err)
			os.Exit(1)
		}
		color.Green("Reset complete. Start TDL again to configure and log in.")
		return
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

	return application.StartAttached(exe, os.Args[1:], cwd)
}

func startUpdate(plan types.UpdatePlan) error {
	exe, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "get executable path")
	}
	return application.StartUpdateApply(plan, exe, os.Args[1:])
}
