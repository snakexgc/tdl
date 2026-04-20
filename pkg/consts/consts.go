package consts

import (
	"os"
	"path/filepath"
)

func init() {
	// 获取可执行文件所在目录
	execPath, err := os.Executable()
	if err != nil {
		panic(err)
	}
	execDir := filepath.Dir(execPath)

	HomeDir = execDir
	DataDir = filepath.Join(execDir, ".tdl")
	LogPath = filepath.Join(DataDir, "log")

	for _, p := range []string{DataDir, LogPath} {
		if err = os.MkdirAll(p, 0o755); err != nil {
			panic(err)
		}
	}
}
