package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
)

const internalDownloadFallbackDirName = "docnload"

func prepareInternalOutputRoot(cfg *config.Config) (root string, fallback bool, err error) {
	if cfg == nil {
		cfg = config.Get()
	}
	configured := ""
	if cfg != nil {
		configured = strings.TrimSpace(cfg.Aria2.Dir)
	}
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

func prepareInternalPartialFile(path string, total int64) (int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if stat.IsDir() {
		return 0, fmt.Errorf("%q is a directory", path)
	}
	size := stat.Size()
	if total > 0 && size > total {
		if err := os.Truncate(path, 0); err != nil {
			return 0, errors.Wrap(err, "truncate oversized partial file")
		}
		return 0, nil
	}
	return size, nil
}

func internalDownloadDirData(task *httpdl.Task) downloadDirData {
	id := strconv.FormatInt(task.PeerID, 10)
	if id == "0" && task.Peer != nil {
		id = strconv.FormatInt(tutil.GetInputPeerID(task.Peer), 10)
	}
	if id == "0" {
		id = task.ID
	}
	return downloadDirData{
		ID:   id,
		Name: safePathSegment(id),
		Time: time.Now(),
	}
}
