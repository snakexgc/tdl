package kv

import (
	"context"
	"io"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/internal/core/storage"
)

type Driver string

const (
	DriverBolt Driver = "bolt"
	DriverFile Driver = "file"
)

func (d Driver) String() string { return string(d) }

type Storage interface {
	Name() string
	Snapshot(context.Context, string) (map[string][]byte, error)
	Namespaces() ([]string, error)
	Open(ns string) (storage.Storage, error)
	io.Closer
}

// New opens a supported storage backend at an explicit filesystem path.
func New(driver Driver, path string) (Storage, error) {
	if path == "" {
		return nil, errors.New("storage path is required")
	}
	switch driver {
	case DriverBolt:
		return newBolt(path)
	case DriverFile:
		return newFile(path)
	default:
		return nil, errors.Errorf("unsupported driver: %s", driver)
	}
}

type ctxKey struct{}

func With(ctx context.Context, kv Storage) context.Context {
	return context.WithValue(ctx, ctxKey{}, kv)
}

func From(ctx context.Context) Storage {
	return ctx.Value(ctxKey{}).(Storage)
}
