package webui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/app/watch"
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
	downloaderMode := config.EffectiveDownloaderMode(cfg)
	statusByGID := map[string]aria2Status{}
	var statusErrText string
	if downloaderMode == config.DownloaderModeAria2 && strings.TrimSpace(cfg.Aria2.RPCURL) != "" {
		var statusErr error
		statusByGID, statusErr = s.aria2Observer().Observe(ctx)
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
		if task.ID != "" && task.ID != id {
			continue
		}
		task.ID = id
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
			if st, ok := statusByGID[record.GID]; ok {
				entry.Status = normalizedAria2Status(st.Status)
				entry.Total, entry.Completed = aria2Lengths(st)
				entry.Error = strings.TrimSpace(strings.TrimSpace(st.ErrorCode + " " + st.ErrorMessage))
			}
			observation.Aria2 = append(observation.Aria2, entry)
		}
		for _, internal := range internalByTask[task.ID] {
			entry := types.InternalLinkEntry{
				ID:        internal.ID,
				Status:    internal.Status,
				Path:      internal.Path,
				Total:     internal.Total,
				Completed: internal.Completed,
				Error:     internal.Error,
				CreatedAt: internal.CreatedAt,
				UpdatedAt: internal.UpdatedAt,
			}
			observation.Internal = append(observation.Internal, entry)
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
	return ports.LinkSubmissionResources{Records: records, Mode: config.EffectiveDownloaderMode(&cfg), PublicBaseURL: cfg.HTTP.PublicBaseURL, RemoteDir: cfg.Aria2.Dir, Limit: config.EffectiveLimit(&cfg), Connections: config.EffectivePoolSize(&cfg), Local: catalogLocalExecutor{a.server.internalDownloadController(), &cfg}, Remote: aria2rpc.NewClient(cfg.Aria2), Repository: taskhub.NewAria2Repository(a.repository.Store, 0)}, nil
}

type catalogLocalExecutor struct {
	controller *watch.InternalDownloadController
	cfg        *config.Config
}

func (catalogLocalExecutor) Name() string { return localDownloadExecutor }
func (e catalogLocalExecutor) Submit(ctx context.Context, request types.DownloadSubmission) (types.DownloadResult, error) {
	info, err := e.controller.AddLink(ctx, e.cfg, request.TaskID)
	return types.DownloadResult{Account: request.Account, Target: e.Name(), ID: info.ID}, err
}
