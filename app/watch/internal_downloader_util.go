package watch

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/core/storage"
)

func shouldRunInternalDownload(status string) bool {
	switch status {
	case "", InternalDownloadStatusQueued, InternalDownloadStatusError:
		return true
	default:
		return false
	}
}

func shouldPauseInternalDownloadForShutdown(status string) bool {
	switch status {
	case InternalDownloadStatusComplete, InternalDownloadStatusRemoved, InternalDownloadStatusPaused:
		return false
	default:
		return true
	}
}

func internalDownloadInfo(record internalDownloadRecord) InternalDownloadInfo {
	info := InternalDownloadInfo{
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
		if record.Status == InternalDownloadStatusActive {
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

func markPersistentDownloadTaskDownloaded(ctx context.Context, kvd storage.Storage, taskID string) {
	if kvd == nil || taskID == "" {
		return
	}
	key := httpdl.TaskStorageKey(taskID)
	data, err := kvd.Get(ctx, key)
	if err != nil {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	if downloaded, _ := raw["downloaded"].(bool); downloaded {
		return
	}
	raw["downloaded"] = true
	data, err = json.Marshal(raw)
	if err != nil {
		return
	}
	_ = kvd.Set(ctx, key, data)
}
