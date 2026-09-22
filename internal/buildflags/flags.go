// Package buildflags shares release linker flags with Docker and GoReleaser.
package buildflags

import (
	_ "embed"
	"fmt"
	"strings"
	"unicode"
)

//go:embed ldflags.txt
var linkerTemplate string

func Render(version, commit, date, arm string) (string, error) {
	for _, value := range []string{version, commit, date, arm} {
		if strings.ContainsFunc(value, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("\"'`", r)
		}) {
			return "", fmt.Errorf("build metadata must not contain whitespace, control characters or quotes")
		}
	}
	return fmt.Sprintf(strings.TrimSpace(linkerTemplate), version, commit, date, arm), nil
}
