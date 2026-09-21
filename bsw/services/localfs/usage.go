package localfs

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Usage reports logical file sizes, not allocated blocks. Directory symlinks
// are not traversed; free space belongs to the filesystem containing Root.
type Usage struct {
	Root       string            `json:"root"`
	Exists     bool              `json:"exists"`
	TotalBytes uint64            `json:"total_bytes"`
	FreeBytes  uint64            `json:"free_bytes"`
	FileBytes  int64             `json:"file_bytes"`
	FileCount  int64             `json:"file_count"`
	Errors     map[string]string `json:"errors,omitempty"`
}

func DownloadUsage(ctx context.Context, root string, incomplete []string) Usage {
	result := Usage{Root: root, Errors: map[string]string{}}
	// Downloads create their directory lazily. Query an existing ancestor on
	// the same volume without creating anything just to display statistics.
	probe := root
	for {
		info, err := os.Stat(probe)
		if err == nil {
			if !info.IsDir() {
				result.Errors["directory"] = fmt.Sprintf("%s is not a directory", probe)
				probe = filepath.Dir(probe)
			}
			result.Exists = probe == root
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(probe) == probe {
			result.Errors["directory"] = err.Error()
			break
		}
		probe = filepath.Dir(probe)
	}
	if total, free, err := volumeUsage(ctx, probe); err != nil {
		result.Errors["disk"] = err.Error()
	} else {
		result.TotalBytes, result.FreeBytes = total, free
	}
	if !result.Exists || result.Errors["directory"] != "" {
		return result
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		result.Errors["directory"] = err.Error()
		return result
	}
	excluded := make(map[string]bool, len(incomplete))
	for _, path := range incomplete {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		excluded[pathKey(path)] = true
	}
	err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || excluded[pathKey(path)] || strings.HasPrefix(entry.Name(), ".tdl-write-test-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			result.FileCount++
			result.FileBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		// Never present a partial traversal as a complete directory total.
		result.Errors["directory"] = err.Error()
	}
	return result
}

func pathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
