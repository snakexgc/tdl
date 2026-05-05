package consts

import (
	"os"
	"path/filepath"
	"strings"
)

const EnvHome = "TDL_HOME"

func init() {
	homeDir := strings.TrimSpace(os.Getenv(EnvHome))
	if homeDir == "" {
		// 获取可执行文件所在目录
		execPath, err := os.Executable()
		if err != nil {
			panic(err)
		}
		homeDir = filepath.Dir(execPath)
	}
	if abs, err := filepath.Abs(homeDir); err == nil {
		homeDir = abs
	}

	HomeDir = homeDir
	DataDir = filepath.Join(homeDir, ".tdl")
	LogPath = filepath.Join(DataDir, "log")

	for _, p := range []string{DataDir, LogPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			panic(err)
		}
	}
}
