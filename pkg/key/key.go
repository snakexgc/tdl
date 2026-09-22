package key

import (
	"github.com/snakexgc/tdl/internal/core/storage/keygen"
)

func App() string {
	return keygen.New("app")
}
