package cmd

import (
	"context"

	"github.com/fatih/color"
	"github.com/go-faster/errors"

	"github.com/iyear/tdl/pkg/config"
)

func ensureStartupNTP(ctx context.Context) error {
	selection, err := config.SelectAndSaveStartupNTP(ctx)
	if err != nil {
		return errors.Wrap(err, "select ntp server")
	}

	switch {
	case selection.Host != "":
		color.Green("NTP server: %s", config.FormatNTPSelection(selection))
	case selection.Source == "system":
		color.Yellow("No reachable NTP server found, using system time.")
	}
	return nil
}
