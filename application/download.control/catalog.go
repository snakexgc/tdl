package downloadcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func (s *Catalog) List(ctx context.Context, account types.AccountID) ([]types.DownloadLinkItem, string, error) {
	if err := s.validate(ctx, account); err != nil {
		return nil, "", err
	}
	snapshot, err := s.source.Observe(ctx)
	if err != nil {
		return nil, "", err
	}
	snapshot.Records = slices.Clone(snapshot.Records)
	sort.Slice(snapshot.Records, func(i, j int) bool { return snapshot.Records[i].Key < snapshot.Records[j].Key })
	items := make([]types.DownloadLinkItem, 0, len(snapshot.Records))
	now := time.Now()
	for _, record := range snapshot.Records {
		if err := ctx.Err(); err != nil {
			return nil, snapshot.StatusError, err
		}
		task := record.Task
		record.Aria2 = slices.Clone(record.Aria2)
		record.Internal = slices.Clone(record.Internal)
		item := types.DownloadLinkItem{ID: task.ID, Key: record.Key, URL: DownloadURL(snapshot.PublicBaseURL, task.ID), FileName: task.FileName, FileSize: task.FileSize, PeerID: task.PeerID, MessageID: task.MessageID, CreatedAt: task.CreatedAt, Downloaded: task.Downloaded || record.HTTPCompleted, HTTPDownloaded: record.HTTPCompleted, HTTPDeliveredBytes: record.HTTPDeliveredBytes, Status: "not_submitted", Aria2: record.Aria2, Internal: record.Internal}
		if record.HTTPCompleted {
			stamp := record.HTTPCompletedAt
			item.HTTPDownloadedAt = &stamp
		}
		base := task.CreatedAt
		if !task.LastActiveAt.IsZero() {
			base = task.LastActiveAt
		}
		if snapshot.TTL <= 0 {
			item.Permanent = true
		} else if !base.IsZero() {
			expires := base.Add(snapshot.TTL)
			item.ExpiresAt = &expires
			item.Expired = !expires.After(now)
		}
		for i := range item.Aria2 {
			entry := &item.Aria2[i]
			entry.Downloaded = entry.Status == statusComplete && (entry.Total == 0 || entry.Completed >= entry.Total)
			item.Downloaded = item.Downloaded || entry.Downloaded
			item.Status = entry.Status
		}
		for _, entry := range item.Internal {
			if entry.Status == statusComplete && (entry.Total == 0 || entry.Completed >= entry.Total) {
				item.Downloaded = true
			}
			item.Status = entry.Status
		}
		if item.Downloaded && !task.Downloaded {
			if err := s.source.MarkDownloaded(ctx, task.ID); err != nil {
				if snapshot.StatusError != "" {
					snapshot.StatusError += "; "
				}
				snapshot.StatusError += fmt.Sprintf("%s: persist download status: %v", task.ID, err)
			}
		}
		items = append(items, item)
	}
	return items, snapshot.StatusError, nil
}

type Catalog struct {
	account types.AccountID
	source  ports.CatalogSource
}

func NewCatalog(account types.AccountID, source ports.CatalogSource) *Catalog {
	return &Catalog{account, source}
}
func validLinkID(id string) bool { return id != "" && id != "index" && !strings.ContainsAny(id, "/\\") }
func (s *Catalog) validate(ctx context.Context, account types.AccountID) error {
	if account != s.account {
		return errors.New("download catalog account mismatch")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.source == nil {
		return errors.New("download catalog is not configured")
	}
	return nil
}

func (s *Catalog) Submit(ctx context.Context, account types.AccountID, ids []string) types.LinkSubmissionResult {
	result := types.LinkSubmissionResult{OK: true}
	if err := s.validate(ctx, account); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	resources, err := s.source.Submission(ctx)
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	if resources.Mode != localExecutor && resources.Mode != aria2Executor {
		result.OK = false
		result.Errors = append(result.Errors, "unsupported download executor")
		return result
	}
	if (resources.Mode == localExecutor && resources.Local == nil) || (resources.Mode != localExecutor && (resources.Remote == nil || resources.Repository == nil)) {
		result.OK = false
		result.Errors = append(result.Errors, "download executor is not configured")
		return result
	}
	aria2Configured := false

	seen := make(map[string]bool)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			result.Errors = append(result.Errors, err.Error())
			break
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !validLinkID(id) {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: reserved download metadata key", id))
			continue
		}
		if seen[id] {
			result.Skipped++
			continue
		}
		seen[id] = true
		var task types.PersistentLink
		data, ok := resources.Records[id]
		if !ok {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: link record not found", id))
			continue
		}
		if err := json.Unmarshal(data, &task); err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if task.ID != "" && task.ID != id {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: record id mismatch", id))
			continue
		}
		task.ID = id
		if resources.Mode == localExecutor {
			if _, err := resources.Local.Submit(ctx, types.DownloadSubmission{Account: account, TaskID: task.ID}); err != nil {
				result.Skipped++
				result.Errors = appendInternalDownloadError(result.Errors, id, data, err)
				continue
			}
			result.Added++
			continue
		}
		if !aria2Configured {
			if err := resources.Remote.SetMaxConcurrentDownloads(ctx, resources.Limit); err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: configure aria2 max concurrent downloads: %v", id, err))
				continue
			}
			aria2Configured = true
		}
		link := DownloadURL(resources.PublicBaseURL, task.ID)
		gid, err := resources.Remote.AddURI(ctx, link, types.Aria2AddURIOptions{Dir: resources.RemoteDir, Out: task.FileName, Connections: resources.Connections})
		if err == nil && gid == "" {
			err = errors.New("aria2 returned empty gid")
		}
		if err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		result.Added++
		if err := resources.Repository.Add(ctx, types.Aria2TaskRecord{
			GID:         gid,
			TaskID:      task.ID,
			DownloadURL: link,
			Dir:         resources.RemoteDir,
			Out:         task.FileName,
			CreatedAt:   time.Now(),
		}); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: aria2 accepted gid %s but persist aria2 record failed: %v", id, gid, err))
			continue
		}
	}
	result.OK = len(result.Errors) == 0
	return result
}

func hasPersistentDownloadMedia(data []byte) bool {
	var raw struct {
		Media struct {
			Location struct {
				Kind string `json:"kind"`
			} `json:"location"`
		} `json:"media"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	return strings.TrimSpace(raw.Media.Location.Kind) != ""
}

func internalDownloadMetadataError(id string) string {
	return fmt.Sprintf("%s: 下载链接缺少媒体定位信息，无法加入内部下载队列；请删除该 KV 记录后重新触发表情生成下载链接", id)
}

func isRestorePersistentDownloadTaskError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "restore persistent download task")
}

func appendInternalDownloadError(errorsList []string, id string, data []byte, err error) []string {
	if isRestorePersistentDownloadTaskError(err) && !hasPersistentDownloadMedia(data) {
		return append(errorsList, internalDownloadMetadataError(id))
	}
	return append(errorsList, fmt.Sprintf("%s: %v", id, err))
}

func DownloadURL(baseURL, taskID string) string {
	if baseURL == "" {
		return "/download/" + url.PathEscape(taskID)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "/download/" + url.PathEscape(taskID)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/download/" + url.PathEscape(taskID)
	return u.String()
}
