// swc-check validates the component composition without opening configuration,
// databases, Telegram connections, or HTTP listeners.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func main() {
	empty := flag.Bool("empty", false, "validate an empty component registry")
	flag.Parse()
	registry := rte.NewRegistry()
	if !*empty {
		var err error
		registry, err = application.Registry()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := check(registry); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(registry *rte.Registry) error {
	run, err := registry.Build(types.DefaultAccount, nil, nil)
	if err != nil {
		return err
	}
	ctx := context.Background()
	statuses := run.Start(ctx)
	for _, status := range statuses {
		if status.State != rte.Running {
			_ = run.Stop(ctx)
			return fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	fmt.Printf("已装载 %d 个 SWC，装配校验通过\n", len(statuses))
	return run.Stop(ctx)
}
