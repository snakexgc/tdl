package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/errors"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
)

func (s *Server) handleInternalDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	controller := s.internalDownloadController()
	items, err := controller.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	overview, _ := controller.Overview(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		fieldItems: items,
		"overview": overview,
	})
}

func (s *Server) handleInternalDownloadActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Action   string   `json:"action"`
		IDs      []string `json:"ids"`
		Statuses []string `json:"statuses"` // used by delete_all to filter by status
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	controller := s.internalDownloadController()
	action := strings.ToLower(strings.TrimSpace(req.Action))
	var (
		result watch.InternalDownloadActionResult
		err    error
	)
	switch action {
	case "pause":
		result, err = controller.Pause(r.Context(), req.IDs)
	case "start":
		result, err = controller.Start(r.Context(), req.IDs)
	case actionDelete:
		result, err = controller.Delete(r.Context(), req.IDs)
	case "pause_all":
		result, err = controller.PauseAll(r.Context())
	case "start_all":
		result, err = controller.StartAll(r.Context())
	case "delete_all":
		// Statuses filters which status groups to delete.
		// Empty → defaults to complete + error (safe purge).
		result, err = controller.DeleteAllByStatus(r.Context(), req.Statuses)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported action %q", req.Action))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     len(result.Errors) == 0,
		"result": result,
	})
}

func (s *Server) handleKVLinks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, statusErr, err := s.listDownloadLinks(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			fieldItems:     items,
			"status_error": statusErr,
		})
	default:
		methodNotAllowed(w, "GET")
	}
}

func (s *Server) handleKVLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, "DELETE")
		return
	}
	if s.opts.NamespaceKV == nil {
		writeError(w, http.StatusInternalServerError, errors.New("namespace kv storage is not configured"))
		return
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/kv/links/"))
	if err != nil || id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	deleted, err := s.deleteDownloadLink(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldDeleted: deleted})
}

func (s *Server) handleKVActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	req.Action = strings.TrimSpace(strings.ToLower(req.Action))
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no links selected"))
		return
	}

	switch req.Action {
	case actionDelete:
		var deleted int
		var itemErrors []string
		for _, id := range req.IDs {
			n, err := s.deleteDownloadLink(r.Context(), id)
			if err != nil {
				itemErrors = append(itemErrors, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			deleted += n
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      len(itemErrors) == 0,
			"deleted": deleted,
			"errors":  itemErrors,
		})
	case "download":
		result := s.downloadLinks(r.Context(), req.IDs)
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported action %q", req.Action))
	}
}

type downloadLinkItem struct {
	ID         string              `json:"id"`
	Key        string              `json:"key"`
	URL        string              `json:"url"`
	FileName   string              `json:"file_name"`
	FileSize   int64               `json:"file_size"`
	PeerID     int64               `json:"peer_id"`
	MessageID  int                 `json:"message_id"`
	CreatedAt  time.Time           `json:"created_at"`
	ExpiresAt  *time.Time          `json:"expires_at,omitempty"`
	Permanent  bool                `json:"permanent"`
	Expired    bool                `json:"expired"`
	Downloaded bool                `json:"downloaded"`
	Status     string              `json:"status"`
	Aria2      []aria2LinkEntry    `json:"aria2"`
	Internal   []internalLinkEntry `json:"internal"`
}

type aria2LinkEntry struct {
	GID         string    `json:"gid"`
	Status      string    `json:"status"`
	Downloaded  bool      `json:"downloaded"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	CreatedAt   time.Time `json:"created_at"`
	Total       int64     `json:"total"`
	Completed   int64     `json:"completed"`
	Error       string    `json:"error,omitempty"`
}

type internalLinkEntry struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Path      string    `json:"path"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type persistentDownloadTask struct {
	ID           string    `json:"id"`
	PeerID       int64     `json:"peer_id"`
	MessageID    int       `json:"message_id"`
	FileName     string    `json:"file_name"`
	FileSize     int64     `json:"file_size"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at,omitempty"`
	Downloaded   bool      `json:"downloaded"`
}

func (s *Server) listDownloadLinks(ctx context.Context) ([]downloadLinkItem, string, error) {
	if s.opts.KVEngine == nil {
		return nil, "", errors.New("kv engine is not configured")
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return nil, "", errors.Wrap(err, "list kv keys")
	}
	pairs := meta[s.namespace()]

	records, recordsByTask, err := s.parseAria2Records(pairs)
	if err != nil {
		return nil, "", err
	}

	cfg := config.Get()
	downloaderMode := config.EffectiveDownloaderMode(cfg)
	statusByGID := map[string]aria2Status{}
	var statusErrText string
	if downloaderMode == config.DownloaderModeAria2 {
		var statusErr error
		statusByGID, statusErr = fetchAria2Statuses(ctx, cfg.Aria2)
		if statusErr != nil {
			statusErrText = statusErr.Error()
		} else {
			s.discoverAria2RecordsFromDownloadLinks(ctx, pairs, records, recordsByTask, statusByGID, cfg)
		}
	}
	internalByTask := map[string][]watch.InternalDownloadInfo{}
	if s.opts.NamespaceKV != nil {
		internalItems, err := s.internalDownloadController().List(ctx)
		if err != nil {
			if statusErrText == "" {
				statusErrText = err.Error()
			}
		} else {
			for _, item := range internalItems {
				taskID := item.TaskID
				if taskID == "" {
					taskID = item.ID
				}
				internalByTask[taskID] = append(internalByTask[taskID], item)
			}
		}
	}

	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		if isDownloadTaskRecordKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	now := time.Now()
	items := make([]downloadLinkItem, 0, len(keys))
	for _, key := range keys {
		var task persistentDownloadTask
		if err := json.Unmarshal(pairs[key], &task); err != nil {
			continue
		}
		if task.ID == "" {
			task.ID = strings.TrimPrefix(key, downloadTaskKeyPrefix)
		}

		item := downloadLinkItem{
			ID:         task.ID,
			Key:        key,
			URL:        downloadURL(cfg.HTTP.PublicBaseURL, task.ID),
			FileName:   task.FileName,
			FileSize:   task.FileSize,
			PeerID:     task.PeerID,
			MessageID:  task.MessageID,
			CreatedAt:  task.CreatedAt,
			Downloaded: task.Downloaded,
			Status:     "not_submitted",
		}
		expiryBase := task.CreatedAt
		if !task.LastActiveAt.IsZero() {
			expiryBase = task.LastActiveAt
		}
		if cfg.HTTP.DownloadLinkTTLHours <= 0 {
			item.Permanent = true
		} else if !expiryBase.IsZero() {
			expiresAt := expiryBase.Add(time.Duration(cfg.HTTP.DownloadLinkTTLHours) * time.Hour)
			item.ExpiresAt = &expiresAt
			item.Expired = !expiresAt.After(now)
		}

		for _, record := range recordsByTask[task.ID] {
			entry := aria2LinkEntry{
				GID:         record.GID,
				Status:      "registered",
				DownloadURL: record.DownloadURL,
				Dir:         record.Dir,
				Out:         record.Out,
				CreatedAt:   record.CreatedAt,
			}
			if record.Status != "" {
				entry.Status = record.Status
				entry.Total = record.Total
				entry.Completed = record.Completed
				entry.Error = record.Error
			}
			if st, ok := statusByGID[record.GID]; ok {
				entry.Status = normalizedAria2Status(st.Status)
				entry.Total, entry.Completed = aria2Lengths(st)
				entry.Error = strings.TrimSpace(strings.TrimSpace(st.ErrorCode + " " + st.ErrorMessage))
			}
			entry.Downloaded = entry.Status == aria2StatusComplete && (entry.Total == 0 || entry.Completed >= entry.Total)
			if entry.Downloaded {
				item.Downloaded = true
				s.markDownloadTaskDownloaded(ctx, task.ID)
			}
			item.Status = entry.Status
			item.Aria2 = append(item.Aria2, entry)
		}
		for _, internal := range internalByTask[task.ID] {
			entry := internalLinkEntry{
				ID:        internal.ID,
				Status:    internal.Status,
				Path:      internal.Path,
				Total:     internal.Total,
				Completed: internal.Completed,
				Error:     internal.Error,
				CreatedAt: internal.CreatedAt,
				UpdatedAt: internal.UpdatedAt,
			}
			if entry.Status == watch.InternalDownloadStatusComplete && (entry.Total == 0 || entry.Completed >= entry.Total) {
				item.Downloaded = true
				s.markDownloadTaskDownloaded(ctx, task.ID)
			}
			item.Status = entry.Status
			item.Internal = append(item.Internal, entry)
		}
		items = append(items, item)
	}

	return items, statusErrText, nil
}

func (s *Server) markDownloadTaskDownloaded(ctx context.Context, taskID string) {
	if s.opts.NamespaceKV == nil || taskID == "" {
		return
	}
	data, err := s.opts.NamespaceKV.Get(ctx, downloadTaskKeyPrefix+taskID)
	if err != nil {
		return
	}
	data, changed, err := markDownloadTaskDataDownloaded(data, taskID)
	if err != nil || !changed {
		return
	}
	_ = s.opts.NamespaceKV.Set(ctx, downloadTaskKeyPrefix+taskID, data)
}

func markDownloadTaskDataDownloaded(data []byte, taskID string) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if downloadedRaw, ok := raw["downloaded"]; ok {
		var downloaded bool
		if err := json.Unmarshal(downloadedRaw, &downloaded); err == nil && downloaded {
			return data, false, nil
		}
	}

	raw["downloaded"] = json.RawMessage(valueTrue)
	if idRaw, ok := raw["id"]; !ok || string(idRaw) == `""` || strings.TrimSpace(string(idRaw)) == "" {
		idData, err := json.Marshal(taskID)
		if err != nil {
			return nil, false, err
		}
		raw["id"] = idData
	}
	updated, err := json.Marshal(raw)
	if err != nil {
		return nil, false, err
	}
	return updated, true, nil
}

// downloadTaskActivity returns the record's sliding expiry base (LastActiveAt,
// falling back to CreatedAt) and whether the record exists.
func (s *Server) downloadTaskActivity(ctx context.Context, taskID string) (time.Time, bool) {
	if s.opts.NamespaceKV == nil || taskID == "" {
		return time.Time{}, false
	}
	data, err := s.opts.NamespaceKV.Get(ctx, downloadTaskKeyPrefix+taskID)
	if err != nil {
		return time.Time{}, false
	}
	var task persistentDownloadTask
	if err := json.Unmarshal(data, &task); err != nil {
		return time.Time{}, false
	}
	if !task.LastActiveAt.IsZero() {
		return task.LastActiveAt, true
	}
	return task.CreatedAt, true
}

// refreshDownloadTaskActivity slides a download link's expiry by stamping
// last_active_at=now on the record, throttled to one write per refresh interval.
// It rewrites only that field via the raw JSON so every other field (media, peer,
// downloaded flag) is preserved.
func (s *Server) refreshDownloadTaskActivity(ctx context.Context, taskID string, now time.Time, ttl time.Duration) {
	if s.opts.NamespaceKV == nil || taskID == "" || ttl <= 0 {
		return
	}
	data, err := s.opts.NamespaceKV.Get(ctx, downloadTaskKeyPrefix+taskID)
	if err != nil {
		return
	}
	updated, changed, err := httpdl.SetDownloadTaskLastActive(data, now, httpdl.RefreshInterval(ttl))
	if err != nil || !changed {
		return
	}
	_ = s.opts.NamespaceKV.Set(ctx, downloadTaskKeyPrefix+taskID, updated)
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

func (s *Server) deleteDownloadLink(ctx context.Context, id string) (int, error) {
	if s.opts.NamespaceKV == nil {
		return 0, errors.New("namespace kv storage is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") || !isDownloadTaskRecordKey(downloadTaskKeyPrefix+id) {
		return 0, errors.New("invalid download link id")
	}

	deleted := 0
	if err := s.opts.NamespaceKV.Delete(ctx, downloadTaskKeyPrefix+id); err != nil {
		return deleted, errors.Wrap(err, "delete download task")
	}
	deleted++

	records, _, err := s.loadAria2Records()
	if err != nil {
		return deleted, nil
	}
	for key, record := range records {
		if record.TaskID != id {
			continue
		}
		if err := s.opts.NamespaceKV.Delete(ctx, key); err != nil {
			return deleted, errors.Wrap(err, "delete aria2 task record")
		}
		deleted++
	}
	result, err := s.internalDownloadController().Delete(ctx, []string{id})
	if err == nil {
		deleted += result.Changed
	}
	return deleted, nil
}

type kvDownloadActionResult struct {
	OK      bool     `json:"ok"`
	Added   int      `json:"added"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

func (s *Server) downloadLinks(ctx context.Context, ids []string) kvDownloadActionResult {
	result := kvDownloadActionResult{OK: true}
	if s.opts.NamespaceKV == nil || s.opts.KVEngine == nil {
		result.OK = false
		result.Errors = append(result.Errors, "kv storage is not configured")
		return result
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	pairs := meta[s.namespace()]
	cfg := config.Get()
	threads := config.EffectiveThreads(cfg)
	limit := config.EffectiveLimit(cfg)
	connections := config.HTTPRangeConnectionsFor(cfg.HTTP, threads)
	downloaderMode := config.EffectiveDownloaderMode(cfg)
	internalController := s.internalDownloadController()
	aria2Configured := false

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !isDownloadTaskRecordKey(downloadTaskKeyPrefix + id) {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: reserved download metadata key", id))
			continue
		}
		var task persistentDownloadTask
		data, ok := pairs[downloadTaskKeyPrefix+id]
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
		if task.ID == "" {
			task.ID = id
		}
		if downloaderMode == config.DownloaderModeInternal {
			if _, err := internalController.AddLink(ctx, cfg, task.ID); err != nil {
				result.Skipped++
				result.Errors = appendInternalDownloadError(result.Errors, id, data, err)
				continue
			}
			result.Added++
			continue
		}
		if !aria2Configured {
			if err := configureAria2MaxConcurrentDownloads(ctx, cfg.Aria2, limit); err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: configure aria2 max concurrent downloads: %v", id, err))
				continue
			}
			aria2Configured = true
		}
		link := downloadURL(cfg.HTTP.PublicBaseURL, task.ID)
		gid, err := addAria2URI(ctx, cfg.Aria2, link, task.FileName, connections)
		if err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if err := s.saveAria2Record(ctx, aria2TaskRecord{
			GID:          gid,
			TaskID:       task.ID,
			DownloadURL:  link,
			Dir:          cfg.Aria2.Dir,
			Out:          task.FileName,
			Connections:  connections,
			TransferMode: config.EffectiveHTTPTransferMode(cfg),
			CreatedAt:    time.Now(),
		}); err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: persist aria2 record: %v", id, err))
			continue
		}
		result.Added++
	}
	result.OK = len(result.Errors) == 0
	return result
}

func downloadURL(baseURL, taskID string) string {
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

func isDownloadTaskRecordKey(key string) bool {
	return strings.HasPrefix(key, downloadTaskKeyPrefix) && key != downloadTaskIndexKey
}
