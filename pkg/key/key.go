package key

import (
	"github.com/iyear/tdl/core/storage/keygen"
)

func App() string {
	return keygen.New("app")
}
