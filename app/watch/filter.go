package watch

import "path/filepath"

func (w *Watcher) matchFilter(name string, size int64) bool {
	if !w.matchExtensionFilter(name) {
		return false
	}
	return w.matchFileSizeFilter(size)
}

func (w *Watcher) matchExtensionFilter(name string) bool {
	ext := filepath.Ext(name)
	if len(w.include) > 0 {
		if _, ok := w.include[ext]; !ok {
			return false
		}
	}
	if len(w.exclude) > 0 {
		if _, ok := w.exclude[ext]; ok {
			return false
		}
	}
	return true
}

func (w *Watcher) matchFileSizeFilter(size int64) bool {
	return w.minFileSizeBytes <= 0 || size >= w.minFileSizeBytes
}

func fileSizeMBToBytes(mb int64) int64 {
	if mb <= 0 {
		return 0
	}
	const maxInt64 = int64(1<<63 - 1)
	if mb > maxInt64/bytesPerMegabyte {
		return maxInt64
	}
	return mb * bytesPerMegabyte
}

func addPrefixDot(v string) string {
	if v == "" || v[0] == '.' {
		return v
	}
	return "." + v
}
