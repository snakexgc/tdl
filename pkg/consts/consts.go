package consts

import (
	"os"
	"path/filepath"
)

func init() {
	dir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	HomeDir = dir
	DataDir = filepath.Join(dir, ".tdl")
	LogPath = filepath.Join(DataDir, "log")

	for _, p := range []string{DataDir} {
		if err = os.MkdirAll(p, 0o755); err != nil {
			panic(err)
		}
	}
}
