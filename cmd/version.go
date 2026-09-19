package cmd

import (
	"bytes"
	_ "embed"
	"runtime"
	"text/template"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/snakexgc/tdl/pkg/consts"
)

//go:embed version.tmpl
var version string

const versionCommand = "version"

func NewVersion() *cobra.Command {
	return &cobra.Command{
		Use:   versionCommand,
		Short: "Check the version info",
		RunE: func(cmd *cobra.Command, args []string) error {
			buf := &bytes.Buffer{}
			if err := template.Must(template.New(versionCommand).Parse(version)).Execute(buf, map[string]interface{}{
				"Version":   consts.Version,
				"Commit":    consts.Commit,
				"Date":      consts.CommitDate,
				"GoVersion": runtime.Version(),
				"GOOS":      runtime.GOOS,
				"GOARCH":    runtime.GOARCH,
				"GOARM":     consts.GOARM,
			}); err != nil {
				return err
			}
			color.Blue(buf.String())
			return nil
		},
	}
}
