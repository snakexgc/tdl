package cmd

import (
	"github.com/spf13/cobra"

	"github.com/iyear/tdl/app/bot"
	"github.com/iyear/tdl/pkg/config"
)

func NewBot() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "bot",
		Short:   "Start a Telegram bot",
		Long:    "Start a Telegram bot that processes messages from allowed users and replies with user ID for unauthorized users.",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Get()
			return bot.Run(cmd.Context(), bot.Options{
				Token:        cfg.Bot.Token,
				AllowedUsers: cfg.Bot.AllowedUsers,
				Proxy:        cfg.Proxy,
			})
		},
	}

	return cmd
}
