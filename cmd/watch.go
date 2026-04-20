package cmd

import (
	"github.com/spf13/cobra"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
)

func NewWatch() *cobra.Command {
	var opts watch.Options

	cmd := &cobra.Command{
		Use:     "watch",
		Short:   "Watch for message reactions and download media automatically",
		Long:    "Watch for message reactions in real-time. When you add a reaction to a message, its media will be downloaded automatically.",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Threads = config.Get().Threads
			return watch.Run(cmd.Context(), opts)
		},
	}

	const (
		include = "include"
		exclude = "exclude"
	)

	cmd.Flags().StringVarP(&opts.Dir, "dir", "d", config.Get().DownloadDir, "download directory")
	cmd.Flags().StringVar(&opts.Template, "template",
		"{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}",
		"download file name template")
	cmd.Flags().BoolVar(&opts.SkipSame, "skip-same", false, "skip files with same name and size")
	cmd.Flags().BoolVar(&opts.RewriteExt, "rewrite-ext", false, "rewrite file extension by MIME")
	cmd.Flags().StringSliceVarP(&opts.Include, include, "i", []string{}, "include the specified file extensions, and only judge by file name, not file MIME. Example: -i mp4,mp3")
	cmd.Flags().StringSliceVarP(&opts.Exclude, exclude, "e", []string{}, "exclude the specified file extensions, and only judge by file name, not file MIME. Example: -e png,jpg")

	cmd.MarkFlagsMutuallyExclusive(include, exclude)

	return cmd
}
