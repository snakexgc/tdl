package webui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/ecual/aria2rpc"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

const localDownloadExecutor = "local"

type catalogAdapter struct {
	server     *Server
	repository taskhub.LinkRepository
}

func (a catalogAdapter) MarkDownloaded(ctx context.Context, id string) error {
	if a.repository.Store == nil {
		return nil
	}
	return taskhub.Links(a.repository.Store).MarkDownloaded(ctx, id)
}

func (a catalogAdapter) Observe(ctx context.Context) (types.LinkCatalogSnapshot, error) {
	s := a.server
	pairs, err := a.repository.Snapshot(ctx)
	if err != nil {
		return types.LinkCatalogSnapshot{}, err
	}
	_, recordsByTask, err := s.parseAria2Records(pairs)
	if err != nil {
		return types.LinkCatalogSnapshot{}, err
	}

	cfg := config.From(s.opts.Context)
	downloaderMode := config.PrimaryDownloadExecutor(cfg)
	var statusErrText string
	if downloaderMode == config.DownloadExecutorAria2 && strings.TrimSpace(cfg.Aria2.RPCURL) != "" {
		_, statusErr := s.aria2Observer().Observe(ctx)
		if statusErr != nil {
			statusErrText = statusErr.Error()
		} else {
			pairs, err = a.repository.Snapshot(ctx)
			if err != nil {
				return types.LinkCatalogSnapshot{}, err
			}
			_, recordsByTask, err = s.parseAria2Records(pairs)
			if err != nil {
				return types.LinkCatalogSnapshot{}, err
			}
		}
	}
	localByTask := map[string][]types.LocalDownloadInfo{}
	if s.opts.NamespaceKV != nil {
		localItems, err := s.localDownloadController().List(ctx)
		if err != nil {
			if statusErrText == "" {
				statusErrText = err.Error()
			}
		} else {
			for _, item := range localItems {
				taskID := item.TaskID
				localByTask[taskID] = append(localByTask[taskID], item)
			}
		}
	}

	snapshot := types.LinkCatalogSnapshot{PublicBaseURL: cfg.HTTP.PublicBaseURL, TTL: time.Duration(cfg.HTTP.DownloadLinkTTLHours) * time.Hour, StatusError: statusErrText}
	for key, data := range pairs {
		if !isDownloadTaskRecordKey(key) {
			continue
		}
		var task types.PersistentLink
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		id := strings.TrimPrefix(key, downloadTaskKeyPrefix)
		if task.ID != id {
			continue
		}
		status, _ := httpdl.ParseDownloadTaskHTTPStatus(data)
		observation := types.LinkCatalogRecord{Key: key, Task: task, HTTPCompleted: status.Completed, HTTPCompletedAt: status.CompletedAt, HTTPDeliveredBytes: status.DeliveredBytes}
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
			// Only render observations accepted by taskhub. The raw RPC reply
			// may have lost a race with pause/delete or source replacement.
			observation.Aria2 = append(observation.Aria2, entry)
		}
		for _, localTask := range localByTask[task.ID] {
			entry := types.LocalLinkEntry{
				ID:        localTask.ID,
				Status:    localTask.Status,
				Path:      localTask.Path,
				Total:     localTask.Total,
				Completed: localTask.Completed,
				Error:     localTask.Error,
				CreatedAt: localTask.CreatedAt,
				UpdatedAt: localTask.UpdatedAt,
			}
			observation.Local = append(observation.Local, entry)
		}

		snapshot.Records = append(snapshot.Records, observation)
	}
	return snapshot, ctx.Err()
}

func (a catalogAdapter) Submission(ctx context.Context) (ports.LinkSubmissionResources, error) {
	if a.repository.Store == nil {
		return ports.LinkSubmissionResources{}, errors.New("kv storage is not configured")
	}
	pairs, err := a.repository.Snapshot(ctx)
	if err != nil {
		return ports.LinkSubmissionResources{}, err
	}
	cfg := *config.From(a.server.opts.Context)
	cfg.Namespace = a.server.namespace()
	records := make(map[string][]byte)
	for key, data := range pairs {
		if isDownloadTaskRecordKey(key) {
			records[strings.TrimPrefix(key, downloadTaskKeyPrefix)] = data
		}
	}
	// Catalog submission must not expire unrelated associations as a side
	// effect. The existing status maintenance runnable owns their cleanup.
	return ports.LinkSubmissionResources{Records: records, Mode: config.PrimaryDownloadExecutor(&cfg), PublicBaseURL: cfg.HTTP.PublicBaseURL, RemoteDir: cfg.Aria2.Dir, Limit: cfg.Limit, Connections: cfg.PoolSize, Local: a.server.opts.LocalLinks, Remote: aria2rpc.NewClient(cfg.Aria2), Repository: taskhub.NewAria2Repository(a.repository.Store, 0)}, nil
}
