package namingrules

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/targetpath"
)

func (r *Rules) Unique(ctx context.Context, input []ports.NamingResult) ([]ports.NamingResult, error) {
	s := r.settings.Load()
	if s == nil {
		return nil, fmt.Errorf("naming component is not initialized")
	}
	result := append([]ports.NamingResult(nil), input...)
	used := make(map[string]struct{}, len(result))
	for i := range result {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		original := result[i].Out
		candidate := original
		maximum := result[i].MaxBytes
		if maximum <= 0 || maximum > 255 {
			maximum = s.maxBytes
		}
		for sequence := 2; ; sequence++ {
			full := targetpath.JoinTargetPath(result[i].Dir, candidate)
			key := strings.ToLower(filepath.Clean(full))
			if _, exists := used[key]; !exists {
				used[key] = struct{}{}
				result[i].Out, result[i].FullPath = candidate, full
				prefix, _ := targetpath.SplitRenderedNameLeaf(result[i].FileName)
				result[i].FileName = prefix + candidate
				break
			}
			ext := filepath.Ext(original)
			suffix := fmt.Sprintf(" (%d)", sequence)
			// Never truncate the distinguishing suffix: doing so can repeat the
			// same candidate forever when the configured byte limit is tiny.
			if len(suffix)+len(ext) > maximum {
				return nil, fmt.Errorf("filename byte limit %d cannot fit a conflict suffix and extension for %q", maximum, original)
			}
			stem := strings.TrimSuffix(original, ext)
			candidate = targetpath.TruncateBytesKeepingRunes(stem, maximum-len(suffix)-len(ext)) + suffix + ext
		}
	}
	return result, nil
}
