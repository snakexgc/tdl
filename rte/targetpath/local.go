package targetpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalRoot resolves an empty local destination beside the running executable,
// independently of the working directory and TDL_HOME. Directory creation is
// deferred until a local download is submitted.
func LocalRoot(configured string) (string, error) {
	root := strings.TrimSpace(configured)
	if root == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate executable for local downloads: %w", err)
		}
		root = filepath.Join(filepath.Dir(executable), "download")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("local download root must be empty or absolute")
	}
	return filepath.Clean(root), nil
}
