package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/snakexgc/tdl/internal/migration"
)

func NewMigrateConfig() *cobra.Command {
	var source, output string
	var write bool
	command := &cobra.Command{Use: migrateConfigCommand, Short: "Validate and export legacy configuration to component documents", Args: cobra.NoArgs}
	command.Flags().StringVar(&source, "source", "config.json", "legacy configuration to read")
	command.Flags().StringVar(&output, "out", "", "new destination directory (parent must exist)")
	command.Flags().BoolVar(&write, "write", false, "write configuration; otherwise preview only")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		file, err := os.Open(source)
		if err != nil {
			return err
		}
		defer file.Close()
		plan, err := migration.Prepare(file)
		if err != nil {
			return err
		}
		if write {
			if output == "" {
				return fmt.Errorf("--out is required with --write")
			}
			err = plan.Write(cmd.Context(), output)
		} else {
			err = plan.Validate(cmd.Context())
		}
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(plan)
	}
	return command
}

const migrateConfigCommand = "migrate-config"
