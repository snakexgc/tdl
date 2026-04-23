package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"go.etcd.io/bbolt"

	"github.com/iyear/tdl/app/bot"
	"github.com/iyear/tdl/cmd"
)

func main() {
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

	args := append([]string{exe}, os.Args[1:]...)
	proc, err := os.StartProcess(exe, args, &os.ProcAttr{
		Dir:   cwd,
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return errors.Wrap(err, "start replacement process")
	}
	return proc.Release()
}
