package watch

import (
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type internalTaskStore = taskhub.LocalRepository

func newInternalTaskStore(kv storage.Storage) *internalTaskStore {
	return taskhub.NewLocalRepository(kv)
}
