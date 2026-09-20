package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-faster/errors"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
)

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

type (
	downloadLinkItem       = types.DownloadLinkItem
	aria2LinkEntry         = types.Aria2LinkEntry
	persistentDownloadTask = types.PersistentLink
)

func (s *Server) listDownloadLinks(ctx context.Context) ([]downloadLinkItem, string, error) {
	return s.downloadCatalogPort.List(ctx, types.AccountID(s.namespace()))
}

func (s *Server) markDownloadTaskDownloaded(ctx context.Context, taskID string) {
	if s.opts.NamespaceKV == nil || taskID == "" {
		return
	}
	_ = taskhub.Links(s.opts.NamespaceKV).MarkDownloaded(ctx, taskID)
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
	_ = taskhub.Links(s.opts.NamespaceKV).Mutate(ctx, taskID, func(data []byte, stamp time.Time) ([]byte, time.Time, error) {
		updated, changed, err := httpdl.SetDownloadTaskLastActive(data, now, httpdl.RefreshInterval(ttl))
		if changed {
			stamp = now
		}
		return updated, stamp, err
	})
}

func (s *Server) deleteDownloadLink(ctx context.Context, id string) (int, error) {
	return s.downloadLinksPort.Remove(ctx, types.AccountID(s.namespace()), id)
}

type kvDownloadActionResult = types.LinkSubmissionResult

func (s *Server) downloadLinks(ctx context.Context, ids []string) kvDownloadActionResult {
	return s.downloadCatalogPort.Submit(ctx, types.AccountID(s.namespace()), ids)
}

func isDownloadTaskRecordKey(key string) bool {
	return strings.HasPrefix(key, downloadTaskKeyPrefix) && key != downloadTaskIndexKey
}
