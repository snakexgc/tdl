package kv

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/go-faster/errors"
	"go.etcd.io/bbolt"
	"go.uber.org/multierr"

	"github.com/snakexgc/tdl/internal/core/storage"
)

type bolt struct {
	path string
	dbs  map[string]*bbolt.DB
	mu   *sync.Mutex
}

func newBolt(path string) (*bolt, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, errors.Wrap(err, "create dir")
	}

	return &bolt{
		path: path,
		dbs:  make(map[string]*bbolt.DB),
		mu:   &sync.Mutex{},
	}, nil
}

func (b *bolt) Name() string {
	return DriverBolt.String()
}

func (b *bolt) Namespaces() ([]string, error) {
	namespaces := make([]string, 0)
	if err := b.walk(func(path string) error {
		namespaces = append(namespaces, filepath.Base(path))
		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "walk")
	}

	return namespaces, nil
}

func (b *bolt) walk(fn func(path string) error) error {
	return filepath.Walk(b.path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return errors.Wrap(err, "walk")
		}
		if info.IsDir() {
			return nil
		}

		return fn(path)
	})
}

func (b *bolt) Open(ns string) (storage.Storage, error) {
	return b.open(ns)
}

func (b *bolt) open(ns string) (*boltNamespace, error) {
	if ns == "" {
		return nil, errors.New("namespace is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if db, ok := b.dbs[ns]; ok {
		return &boltNamespace{db: db, ns: []byte(ns)}, nil
	}

	db, err := bbolt.Open(filepath.Join(b.path, ns), os.ModePerm, boltOptions)
	if err != nil {
		return nil, errors.Wrap(err, "open db")
	}
	if err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(ns))
		return err
	}); err != nil {
		return nil, errors.Wrap(err, "create bucket")
	}

	b.dbs[ns] = db

	return &boltNamespace{db: db, ns: []byte(ns)}, nil
}

func (b *bolt) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var err error
	for _, db := range b.dbs {
		err = multierr.Append(err, db.Close())
	}

	return err
}
