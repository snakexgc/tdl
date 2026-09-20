package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	manager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/pkg/consts"
)

const configInitCommand = "config-init"

// NewConfigInit supports offline migration without starting network services,
// touching sessions or printing any user configuration values.
func NewConfigInit() *cobra.Command {
	var home, components string
	command := &cobra.Command{Use: configInitCommand, Short: "Create or validate tdl_config.json, importing existing settings once", Args: cobra.NoArgs}
	command.Flags().StringVar(&home, "home", consts.HomeDir, "application directory containing tdl_config.json or legacy configuration")
	command.Flags().StringVar(&components, "component-config", "", "legacy component directory for the selected account")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		if _, err := configuration.Open(cmd.Context(), home, components); err != nil {
			return err
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "Configuration ready:", filepath.Join(home, manager.Filename))
		return err
	}
	return command
}
