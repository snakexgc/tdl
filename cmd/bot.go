package cmd

import (
	"github.com/spf13/cobra"

	"github.com/iyear/tdl/app/bot"
	tdlruntime "github.com/iyear/tdl/app/runtime"
)

func NewBot() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "bot",
		Short:   "Start a Telegram bot",
		Long:    "Start a Telegram bot that processes messages from allowed users and replies with user ID for unauthorized users.",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBot(cmd)
		},
	}

	return cmd
}

func runBot(cmd *cobra.Command) error {
	return tdlruntime.Run(cmd.Context(), tdlruntime.Options{
		RequestReboot: bot.RequestReboot,
		RequestUpdate: bot.RequestUpdate,
	})
}
