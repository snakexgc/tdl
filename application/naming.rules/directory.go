package namingrules

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/snakexgc/tdl/rte/targetpath"
)

type downloadDirData struct {
	ID               string
	Name             string
	MessageTitle     string
	MessageID        string
	TriggerMessageID string
	FileName         string
	AlbumID          string
	Time             time.Time
}

const (
	safeMessageTitleMaxRunes  = 80
	safeMessageTitleHeadRunes = 48
	safeMessageTitleTailRunes = 30
	safeMessageTitleMarker    = "..."
)

func renderDownloadDir(pattern string, data downloadDirData) []string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}

	rawSegments := targetpath.SplitPathParts(pattern)
	segments := make([]string, 0, len(rawSegments))
	for _, raw := range rawSegments {
		segment := renderDownloadDirSegment(raw, data)
		segment = targetpath.SafePathSegment(segment)
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func renderDownloadDirSegment(segment string, data downloadDirData) string {
	var b strings.Builder
	for _, r := range segment {
		if r == '&' {
			continue
		}
		if value, ok := downloadTemplateValue(r, data); ok {
			b.WriteString(value)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func downloadTemplateValue(r rune, data downloadDirData) (string, bool) {
	switch r {
	case 'F':
		return strings.TrimSuffix(data.FileName, filepath.Ext(data.FileName)), true
	case 'I':
		return safeMessageTitleSegment(data.MessageTitle), true
	case 'G':
		return data.Name, true
	case 'P':
		return data.ID, true
	case 'S':
		return data.MessageID, true
	case 'R':
		return data.TriggerMessageID, true
	case 'A':
		return data.AlbumID, true
	case 'Y':
		return fmt.Sprintf("%04d", data.Time.Year()), true
	case 'M':
		return fmt.Sprintf("%02d", int(data.Time.Month())), true
	case 'D':
		return fmt.Sprintf("%02d", data.Time.Day()), true
	default:
		return "", false
	}
}

func safeMessageTitleSegment(value string) string {
	return safeMessageTitleSegmentWithMax(value, safeMessageTitleMaxRunes)
}

func safeMessageTitleSegmentWithMax(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return limitFileNameRunes([]rune("untitled"), maxRunes)
	}

	runes := make([]rune, 0, len(value))
	for _, r := range value {
		if isMessageTitleFilenameRune(r) {
			runes = append(runes, r)
		}
	}
	if len(runes) == 0 {
		return limitFileNameRunes([]rune("untitled"), maxRunes)
	}
	return limitFileNameRunes(runes, maxRunes)
}

func isMessageTitleFilenameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		unicode.Is(unicode.Han, r)
}

func limitFileNameRunes(runes []rune, maxRunes int) string {
	if maxRunes <= 0 || len(runes) == 0 {
		return ""
	}
	if len(runes) <= maxRunes {
		return string(runes)
	}

	marker := []rune(safeMessageTitleMarker)
	if maxRunes <= len(marker)+1 {
		return string(runes[:maxRunes])
	}

	available := maxRunes - len(marker)
	tail := min(safeMessageTitleTailRunes, available/2)
	head := min(safeMessageTitleHeadRunes, available-tail)
	if head+tail < available {
		head += available - head - tail
	}

	return string(runes[:head]) + safeMessageTitleMarker + string(runes[len(runes)-tail:])
}
