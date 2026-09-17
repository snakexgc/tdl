// Package targetpath manipulates remote target paths without filesystem I/O.
package targetpath

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/flytam/filenamify"
)

func SafePathSegment(value string) string {
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

func RenderedNameLeafByteLen(rendered string) int {
	_, leaf := SplitRenderedNameLeaf(rendered)
	return len(leaf)
}

// LimitRenderedNameLeafBytes hard-truncates the filename leaf to maxBytes, preserving the
// file extension and never splitting a multi-byte UTF-8 rune.

func LimitRenderedNameLeafBytes(rendered string, maxBytes int) string {
	prefix, leaf := SplitRenderedNameLeaf(rendered)
	return prefix + LimitFileNameSegmentBytes(leaf, maxBytes)
}

func SplitRenderedNameLeaf(rendered string) (prefix, leaf string) {
	lastSlash := strings.LastIndexAny(rendered, `/\`)
	if lastSlash < 0 {
		return "", rendered
	}
	return rendered[:lastSlash+1], rendered[lastSlash+1:]
}

// LimitFileNameSegmentBytes hard-truncates name to maxBytes (UTF-8), keeping the extension intact.

func LimitFileNameSegmentBytes(name string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(name) <= maxBytes {
		return name
	}

	ext := filepath.Ext(name)
	extBytes := len(ext)
	if ext != "" && extBytes < maxBytes {
		base := strings.TrimSuffix(name, ext)
		return TruncateBytesKeepingRunes(base, maxBytes-extBytes) + ext
	}

	return TruncateBytesKeepingRunes(name, maxBytes)
}

// TruncateBytesKeepingRunes shortens s to at most maxBytes without splitting a UTF-8 rune.

func TruncateBytesKeepingRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	var n int
	for _, r := range s {
		size := utf8.RuneLen(r)
		if n+size > maxBytes {
			break
		}
		n += size
	}
	return s[:n]
}

func ResolveTargetPath(baseDir, renderedName string) (dir, out, fullPath string) {
	parts := SplitPathParts(renderedName)
	if len(parts) == 0 {
		out = SafePathSegment(renderedName)
		return baseDir, out, JoinTargetPath(baseDir, out)
	}

	out = parts[len(parts)-1]
	if len(parts) > 1 {
		dir = JoinTargetPath(baseDir, parts[:len(parts)-1]...)
	} else {
		dir = baseDir
	}
	fullPath = JoinTargetPath(dir, out)
	return dir, out, fullPath
}

func SplitPathParts(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || field == "." || field == ".." {
			continue
		}
		parts = append(parts, field)
	}
	return parts
}

func JoinTargetPath(base string, parts ...string) string {
	sep := TargetPathSeparator(base)
	originalBase := base
	base = strings.TrimRight(base, `/\`)
	if base == "" && strings.HasPrefix(originalBase, "/") {
		base = "/"
	}

	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, `/\`)
		if part == "" || part == "." || part == ".." {
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

func TargetPathSeparator(base string) string {
	if LooksWindowsPath(base) {
		return `\`
	}
	return "/"
}

func LooksWindowsPath(path string) bool {
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	if len(path) < 3 {
		return false
	}
	drive := path[0]
	return (drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z') && path[1] == ':' && (path[2] == '/' || path[2] == '\\')
}

func CleanTargetRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if LooksWindowsPath(root) {
		return filepath.Clean(root)
	}
	return PathCleanSlash(root)
}

func PathCleanSlash(path string) string {
	absolute := strings.HasPrefix(path, "/")
	parts := SplitPathParts(path)
	clean := strings.Join(parts, "/")
	if absolute {
		return "/" + clean
	}
	if clean == "" {
		return "."
	}
	return clean
}
