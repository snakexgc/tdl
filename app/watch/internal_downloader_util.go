package watch

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/iyear/tdl/core/storage"
)

func shouldRunInternalDownload(status string) bool {
	switch status {
	case "", InternalDownloadStatusQueued, InternalDownloadStatusActive, InternalDownloadStatusError:
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
	return InternalDownloadInfo(record)
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
	key := downloadTaskStorageKey(taskID)
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
