// swc-migrate is the compatibility entry point for the complete component export.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/snakexgc/tdl/internal/migration"
)

func main() {
	source := flag.String("source", "config.json", "legacy configuration to read")
	output := flag.String("out", "", "new destination directory (parent must exist)")
	write := flag.Bool("write", false, "write configuration; otherwise validate and preview only")
	flag.Parse()
	if err := run(*source, *output, *write); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(source, output string, write bool) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 1<<20 {
		return fmt.Errorf("legacy configuration exceeds size limit")
	}
	plan, err := migration.Prepare(file)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if write {
		if output == "" {
			return fmt.Errorf("-out is required with -write")
		}
		if err := plan.Write(ctx, output); err != nil {
			return err
		}
	} else if err := plan.Validate(ctx); err != nil {
		return err
	}
	preview, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(preview))
	fmt.Println("Source configuration and sessions are unchanged. Keep the source for bootstrap settings and rollback.")
	return nil
}
