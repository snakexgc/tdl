package aria2

import (
	"time"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

const DefaultTaskTTL = taskhub.DefaultAria2TaskTTL

type (
	TaskRecord      = types.Aria2TaskRecord
	aria2TaskRecord = TaskRecord
	TaskStore       = taskhub.Aria2Repository
)

func NewTaskStore(kv storage.Storage, ttl ...time.Duration) *TaskStore {
	return taskhub.NewAria2Repository(kv, ttl...)
}
func StorageKey(id string) string { return taskhub.Aria2StorageKey(id) }
