package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
)

const internalDownloadFallbackDirName = "downloads"

func prepareInternalOutputRoot(cfg *config.Config) (root string, fallback bool, err error) {
	if cfg == nil {
		cfg = config.Get()
	}
	configured := ""
	if cfg != nil {
		configured = strings.TrimSpace(cfg.Downloader.LocalRoot)
	}
	return prepareLocalRoot(configured)
}

func prepareLocalRoot(configured string) (root string, fallback bool, err error) {
	if configured != "" {
		root = filepath.Clean(configured)
		if err := ensureWritableDir(root); err == nil {
			return root, false, nil
		}
	}

	root = filepath.Join(consts.HomeDir, internalDownloadFallbackDirName)
	if err := ensureWritableDir(root); err != nil {
		if configured != "" {
			return "", true, fmt.Errorf("创建内部下载备用目录 %q 失败：%w", root, err)
		}
		return "", true, fmt.Errorf("创建内部下载目录 %q 失败：%w", root, err)
	}
	return root, configured != "", nil
}

func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stat, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("%q 不是目录", dir)
	}
	f, err := os.CreateTemp(dir, ".tdl-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func internalDownloadDirData(task *httpdl.Task) downloadDirData {
	id := strconv.FormatInt(task.PeerID, 10)
	if id == "0" && task.Peer != nil {
		id = strconv.FormatInt(tutil.GetInputPeerID(task.Peer), 10)
	}
	if id == "0" {
		id = task.ID
	}
	return downloadDirData{ID: id, Name: safePathSegment(id), Time: time.Now()}
}
