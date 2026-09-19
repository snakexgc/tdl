package localfs

import (
	"context"
	"errors"
	"os"
)

// Downloads performs operations on paths explicitly selected as local by the
// application. Remote downloader paths must never be passed to this service.
type Downloads struct{}

func (Downloads) EnsureDirectory(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func (f Downloads) EnsureWritable(ctx context.Context, dir string) error {
	if err := f.EnsureDirectory(ctx, dir); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".tdl-write-test-*")
	if err != nil {
		return err
	}
	return errors.Join(file.Close(), os.Remove(file.Name()))
}

func (Downloads) SameFile(ctx context.Context, path string, size int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular() && info.Size() == size, nil
}
