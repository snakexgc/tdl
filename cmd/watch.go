package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/consts"
)

func NewWatch() *cobra.Command {
	var opts watch.Options

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch for message reactions and download media automatically",
		Long:  "Watch for message reactions in real-time. When you add a reaction to a message, its media will be downloaded automatically.",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Threads = viper.GetInt(consts.FlagThreads)
			return watch.Run(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Dir, "dir", "d", "downloads", "download directory")
	cmd.Flags().StringVar(&opts.Template, "template",
		"{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}",
		"download file name template")
	cmd.Flags().BoolVar(&opts.SkipSame, "skip-same", false, "skip files with same name and size")
	cmd.Flags().BoolVar(&opts.RewriteExt, "rewrite-ext", false, "rewrite file extension by MIME")

	return cmd
}
