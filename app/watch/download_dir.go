package watch

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"

	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
)

type aria2GlobalDirGetter interface {
	GetGlobalDir(ctx context.Context) (string, error)
}

type downloadDirData struct {
	ID   string
	Name string
	Time time.Time
}

func prepareAria2OutputRoot(ctx context.Context, client aria2GlobalDirGetter, cfg *config.Config) (root string, ensureDirs bool, err error) {
	if cfg == nil {
		cfg = config.Get()
	}

	if strings.TrimSpace(cfg.Aria2.Dir) != "" {
		// aria2.dir belongs to the filesystem of the aria2 process. tdl may be
		// running on another machine (or another container), so it must never
		// create or inspect this path locally.
		return cleanTargetRoot(cfg.Aria2.Dir), false, nil
	}

	root, err = client.GetGlobalDir(ctx)
	if err != nil {
		return "", false, errors.Wrap(err, "读取 aria2 默认下载目录失败")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	return cleanTargetRoot(root), false, nil
}

func (w *Watcher) downloadDirData(ctx context.Context, file fileTask) downloadDirData {
	id := strconv.FormatInt(tutil.GetInputPeerID(file.peer), 10)
	if id == "0" {
		id = strconv.FormatInt(file.peerID, 10)
	}

	name := id
	if w.manager != nil && file.peer != nil {
		peer, err := w.manager.FromInputPeer(ctx, file.peer)
		if err == nil && peer != nil {
			if resolved := peerTemplateName(peer); resolved != "" {
				name = resolved
			}
		}
	}

	return downloadDirData{ID: id, Name: safePathSegment(name), Time: time.Now()}
}

func peerTemplateName(peer peers.Peer) string {
	switch p := peer.(type) {
	case peers.User:
		if username, ok := p.Username(); ok && username != "" {
			return username
		}
		return p.VisibleName()
	case peers.Chat:
		return p.VisibleName()
	case peers.Channel:
		if name := p.VisibleName(); name != "" {
			return name
		}
		if username, ok := p.Username(); ok {
			return username
		}
		return ""
	default:
		if name := peer.VisibleName(); name != "" {
			return name
		}
		if username, ok := peer.Username(); ok {
			return username
		}
		return ""
	}
}
