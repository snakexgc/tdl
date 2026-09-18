package local

import (
	"strings"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

func shouldRunInternalDownload(status string) bool {
	switch status {
	case "", types.InternalDownloadStatusQueued, types.InternalDownloadStatusError:
		return true
	default:
		return false
	}
}

func shouldPauseInternalDownloadForShutdown(status string) bool {
	switch status {
	case types.InternalDownloadStatusComplete, types.InternalDownloadStatusRemoved, types.InternalDownloadStatusPaused:
		return false
	default:
		return true
	}
}

func internalDownloadInfo(record types.LocalDownloadRecord) types.InternalDownloadInfo {
	info := types.InternalDownloadInfo{
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
		if record.Status == types.InternalDownloadStatusActive {
			info.ElapsedSeconds = int64(time.Since(*record.StartedAt).Seconds())
		} else {
			if elapsed := record.UpdatedAt.Sub(*record.StartedAt); elapsed > 0 {
				info.ElapsedSeconds = int64(elapsed.Seconds())
			}
		}
	}
	return info
}

func uniqueInternalDownloadIDs(ids []string) []string {
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
