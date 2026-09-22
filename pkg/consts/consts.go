package consts

import (
	"os"
	"path/filepath"
	"strings"
)

const EnvHome = "TDL_HOME"

var pathError error

func init() {
	homeDir := strings.TrimSpace(os.Getenv(EnvHome))
	if homeDir == "" {
		// 获取可执行文件所在目录
		execPath, err := os.Executable()
		if err != nil {
			pathError = err
			return
		}
		homeDir = filepath.Dir(execPath)
	}
	if abs, err := filepath.Abs(homeDir); err == nil {
		homeDir = abs
	}

	HomeDir = homeDir
	DataDir = filepath.Join(homeDir, ".tdl")
	LogPath = filepath.Join(DataDir, "log")
}

// InitPaths explicitly creates process directories at application startup.
func InitPaths() error {
	if pathError != nil {
		return pathError
	}
	for _, p := range []string{DataDir, LogPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}
