package local

import (
	"fmt"
	"os"

	"github.com/go-faster/errors"
)

func prepareInternalPartialFile(path string, total int64) (int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if stat.IsDir() {
		return 0, fmt.Errorf("%q is a directory", path)
	}
	size := stat.Size()
	if total > 0 && size > total {
		if err := os.Truncate(path, 0); err != nil {
			return 0, errors.Wrap(err, "truncate oversized partial file")
		}
		return 0, nil
	}
	return size, nil
}
