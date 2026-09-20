// Package configfile provides bounded reads and durable configuration replacement.
package configfile

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const MaxSize = 16 << 20

func Read(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("configuration exceeds %d bytes", MaxSize)
	}
	return data, ctx.Err()
}

func Write(ctx context.Context, path string, data []byte, create bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > MaxSize {
		return fmt.Errorf("configuration exceeds %d bytes", MaxSize)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if create {
		// Publish exclusively: never overwrite a configuration created by another
		// process while an import was being prepared.
		if err := os.Link(f.Name(), path); err != nil {
			return err
		}
	} else if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if parent, err := os.Open(dir); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}
