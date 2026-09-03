//go:build windows

package updater

import "github.com/go-faster/errors"

func execUpdatedProcess(_ string, _ []string) error {
	return errors.New("in-place process restart is not supported on Windows")
}
