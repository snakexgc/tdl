package aria2

import "github.com/snakexgc/tdl/internal/core/storage"

func newMemoryTaskStorage() *storage.Memory { return &storage.Memory{} }
