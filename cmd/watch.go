package cmd

import (
	"github.com/spf13/cobra"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
)

func NewWatch() *cobra.Command {
	opts := watch.DefaultOptions(config.Get())

	cmd := &cobra.Command{
		Use:     "watch",
		Short:   "Watch for message reactions and submit media downloads",
		Long:    "Watch for message reactions in real-time. When you add a reaction to a message, its media will be submitted to the configured downloader automatically.",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureStartupNTP(cmd.Context()); err != nil {
				return err
			}
			cfg := config.Get()
			opts.Threads = effectiveWatchThreads(persistentIntFlag(cmd, consts.FlagThreads, config.EffectiveThreads(cfg)))
			opts.Limit = effectiveWatchLimit(persistentIntFlag(cmd, consts.FlagLimit, config.EffectiveLimit(cfg)))
			opts.PoolSize = effectiveWatchPoolSize(persistentIntFlag(cmd, consts.FlagPoolSize, config.EffectivePoolSize(cfg)), cfg)
			return watch.Run(cmd.Context(), opts)
		},
	}

	const (
		include = "include"
		exclude = "exclude"
	)

	cmd.Flags().StringVarP(&opts.Dir, "dir", "d", config.Get().DownloadDir, "download directory")
	cmd.Flags().StringVar(&opts.Template, "template",
		opts.Template,
		"download file name template")
	cmd.Flags().BoolVar(&opts.SkipSame, "skip-same", false, "skip files with same name and size")
	cmd.Flags().StringSliceVarP(&opts.Include, include, "i", config.Get().Include, "include the specified file extensions, and only judge by file name, not file MIME. Example: -i mp4,mp3")
	cmd.Flags().StringSliceVarP(&opts.Exclude, exclude, "e", config.Get().Exclude, "exclude the specified file extensions, and only judge by file name, not file MIME. Example: -e png,jpg")
	cmd.Flags().Int64Var(&opts.FileSizeMB, "file-size-mb", config.Get().FileSizeMB, "skip files smaller than this size in MB after include/exclude filtering; 0 means unlimited")

	cmd.MarkFlagsMutuallyExclusive(include, exclude)

	return cmd
}

func persistentIntFlag(cmd *cobra.Command, name string, fallback int) int {
	if cmd == nil || cmd.Root() == nil || cmd.Root().PersistentFlags() == nil {
		return fallback
	}
	value, err := cmd.Root().PersistentFlags().GetInt(name)
	if err != nil {
		return fallback
	}
	return value
}

func effectiveWatchThreads(value int) int {
	if value < 1 {
		return config.DefaultThreads
	}
	return value
}

func effectiveWatchLimit(value int) int {
	if value < 1 {
		return config.DefaultLimit
	}
	return value
}

func effectiveWatchPoolSize(value int, cfg *config.Config) int {
	if value < 0 {
		return config.EffectivePoolSize(cfg)
	}
	return value
}
