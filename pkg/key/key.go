package key

import (
	"github.com/snakexgc/tdl/core/storage/keygen"
)

func App() string {
	return keygen.New("app")
}
