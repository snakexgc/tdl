package local

import (
	"strings"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

func shouldRunLocalDownload(status string) bool {
	switch status {
	case types.LocalDownloadStatusQueued, types.LocalDownloadStatusError:
		return true
	default:
		return false
	}
}

func shouldPauseLocalDownloadForShutdown(status string) bool {
	switch status {
	case types.LocalDownloadStatusComplete, types.LocalDownloadStatusRemoved, types.LocalDownloadStatusPaused:
		return false
	default:
		return true
	}
}

func localDownloadInfo(record types.LocalDownloadRecord) types.LocalDownloadInfo {
	info := types.LocalDownloadInfo{
		ID:            record.ID,
		TaskID:        record.TaskID,
		FileName:      record.FileName,
		Dir:           record.Dir,
		Out:           record.Out,
		Path:          record.Path,
		Total:         record.Total,
		Completed:     record.Completed,
		Status:        record.Status,
		Error:         record.Error,
		DownloadSpeed: record.DownloadSpeed,
		StartedAt:     record.StartedAt,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
		EtaSeconds:    -1,
	}
	if record.DownloadSpeed > 0 && record.Total > record.Completed {
		info.EtaSeconds = (record.Total - record.Completed) / record.DownloadSpeed
	}
	if record.StartedAt != nil {
		if record.Status == types.LocalDownloadStatusActive {
			info.ElapsedSeconds = int64(time.Since(*record.StartedAt).Seconds())
		} else {
			if elapsed := record.UpdatedAt.Sub(*record.StartedAt); elapsed > 0 {
				info.ElapsedSeconds = int64(elapsed.Seconds())
			}
		}
	}
	return info
}

func uniqueLocalDownloadIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
