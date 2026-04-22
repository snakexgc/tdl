package cmd

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/iyear/tdl/app/bot"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
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
	return bot.Run(cmd.Context(), botOptionsFromConfig(config.Get()))
}

func botOptionsFromConfig(cfg *config.Config) bot.Options {
	return bot.Options{
		Token:            cfg.Bot.Token,
		AllowedUsers:     cfg.Bot.AllowedUsers,
		Proxy:            cfg.Proxy,
		Namespace:        cfg.Namespace,
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
		Watch:            watch.DefaultOptions(cfg),
	}
}
