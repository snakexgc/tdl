package login

import "github.com/snakexgc/tdl/internal/core/storage"

func newMemoryStorage() *storage.Memory { return &storage.Memory{} }
