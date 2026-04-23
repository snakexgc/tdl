package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/flytam/filenamify"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/peers"

	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
)

type aria2GlobalDirGetter interface {
	GetGlobalDir(ctx context.Context) (string, error)
}

type downloadDirData struct {
	ID   string
	Name string
	Time time.Time
}

var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func prepareAria2OutputRoot(ctx context.Context, client aria2GlobalDirGetter, cfg *config.Config) (root string, ensureDirs bool, err error) {
	if cfg == nil {
		cfg = config.Get()
	}

	if strings.TrimSpace(cfg.Aria2.Dir) != "" {
		root = filepath.Clean(cfg.Aria2.Dir)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", false, fmt.Errorf("创建 aria2.dir %q 失败：%w", root, err)
		}
		stat, err := os.Stat(root)
		if err != nil {
			return "", false, fmt.Errorf("检查 aria2.dir %q 失败：%w", root, err)
		}
		if !stat.IsDir() {
			return "", false, fmt.Errorf("aria2.dir %q 不是目录", root)
		}
		return root, true, nil
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

	return downloadDirData{
		ID:   id,
		Name: safePathSegment(name),
		Time: time.Now(),
	}
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

func renderDownloadDir(pattern string, data downloadDirData) []string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}

	rawSegments := splitPathParts(pattern)
	segments := make([]string, 0, len(rawSegments))
	for _, raw := range rawSegments {
		segment := renderDownloadDirSegment(raw, data)
		segment = safePathSegment(segment)
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func renderDownloadDirSegment(segment string, data downloadDirData) string {
	var b strings.Builder
	for _, r := range segment {
		switch r {
		case '&':
			continue
		case 'G':
			b.WriteString(data.Name)
		case 'I':
			b.WriteString(data.ID)
		case 'Y':
			fmt.Fprintf(&b, "%04d", data.Time.Year())
		case 'M':
			fmt.Fprintf(&b, "%02d", int(data.Time.Month()))
		case 'D':
			fmt.Fprintf(&b, "%02d", data.Time.Day())
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	safe, err := filenamify.FilenamifyV2(value)
	if err != nil || safe == "" {
		return "invalid-filename"
	}
	return safe
}

func resolveTargetPath(baseDir, renderedName string) (dir, out, fullPath string) {
	parts := splitPathParts(renderedName)
	if len(parts) == 0 {
		out = safePathSegment(renderedName)
		return baseDir, out, joinTargetPath(baseDir, out)
	}

	out = parts[len(parts)-1]
	if len(parts) > 1 {
		dir = joinTargetPath(baseDir, parts[:len(parts)-1]...)
	} else {
		dir = baseDir
	}
	fullPath = joinTargetPath(dir, out)
	return dir, out, fullPath
}

func splitPathParts(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || field == "." {
			continue
		}
		parts = append(parts, field)
	}
	return parts
}

func joinTargetPath(base string, parts ...string) string {
	sep := targetPathSeparator(base)
	originalBase := base
	base = strings.TrimRight(base, `/\`)
	if base == "" && strings.HasPrefix(originalBase, "/") {
		base = "/"
	}

	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, `/\`)
		if part == "" || part == "." {
			continue
		}
		cleanParts = append(cleanParts, part)
	}

	if base == "/" {
		if len(cleanParts) == 0 {
			return "/"
		}
		return "/" + strings.Join(cleanParts, "/")
	}

	withBase := make([]string, 0, len(cleanParts)+1)
	if base != "" {
		withBase = append(withBase, base)
	}
	withBase = append(withBase, cleanParts...)
	if len(withBase) == 0 {
		return ""
	}
	return strings.Join(withBase, sep)
}

func targetPathSeparator(base string) string {
	if looksWindowsPath(base) {
		return `\`
	}
	return "/"
}

func looksWindowsPath(path string) bool {
	return windowsDrivePath.MatchString(path) || strings.HasPrefix(path, `\\`)
}

func cleanTargetRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if looksWindowsPath(root) {
		return filepath.Clean(root)
	}
	return pathCleanSlash(root)
}

func pathCleanSlash(path string) string {
	absolute := strings.HasPrefix(path, "/")
	parts := splitPathParts(path)
	clean := strings.Join(parts, "/")
	if absolute {
		return "/" + clean
	}
	if clean == "" {
		return "."
	}
	return clean
}
