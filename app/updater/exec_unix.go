//go:build !windows

package updater

import (
	"os"

	"golang.org/x/sys/unix"
)

func execUpdatedProcess(path string, args []string) error {
	processArgs := append([]string{path}, args...)
	return unix.Exec(path, processArgs, os.Environ())
}
