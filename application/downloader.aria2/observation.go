package aria2

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/targetpath"
)

type Observer struct {
	Logger        *zap.Logger
	Client        ports.Aria2ControlClient
	Repository    ports.Aria2Observations
	PublicBaseURL string
	TTL           time.Duration
	links         *atomic.Pointer[linkPolicy]
}

func (o Observer) configured() Observer {
	if o.links != nil {
		if policy := o.links.Load(); policy != nil {
			o.PublicBaseURL, o.TTL = policy.baseURL, policy.ttl
		}
		o.links = nil // one immutable policy for the entire observation
	}
	return o
}

// Observe returns the raw RPC snapshot, including observations rejected by the
// repository's revision checks. Use a fresh repository snapshot for task state.
// The repository snapshot always precedes network I/O for optimistic updates.
func (o Observer) Observe(ctx context.Context) (map[string]types.Aria2DownloadStatus, error) {
	o = o.configured()
	snapshot, err := o.Repository.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	statuses := map[string]types.Aria2DownloadStatus{}
	active, err := o.Client.TellActive(ctx)
	if err != nil {
		return statuses, err
	}
	waiting, err := listTaskPages(ctx, o.Client.TellWaiting)
	if err != nil {
		return statuses, err
	}
	stopped, err := listTaskPages(ctx, o.Client.TellStopped)
	if err != nil {
		return statuses, err
	}
	for _, group := range [][]types.Aria2DownloadStatus{active, waiting, stopped} {
		for _, status := range group {
			if status.GID != "" {
				statuses[status.GID] = status
			}
		}
	}
	var result error
	for gid, status := range statuses {
		record, exists := snapshot.Records[gid]
		if record.Deleted {
			continue
		}
		if !exists {
			if status.Status == string(types.DownloadRemoved) {
				continue
			}
			id, uri := o.match(status, snapshot.Links)
			if id == "" {
				continue
			}
			record = types.Aria2TaskRecord{GID: gid, TaskID: id, DownloadURL: uri, CreatedAt: time.Now()}
			if len(status.Files) > 0 {
				record.Dir, record.Out = targetpath.SplitRenderedNameLeaf(status.Files[0].Path)
				record.Dir = strings.TrimRight(record.Dir, `/\`)
			}
		}
		previousStatus := record.Status
		record.Status = normalizedAria2Status(status.Status)
		info := aria2TaskInfo(status)
		record.Total, record.Completed = info.TotalLength, info.CompletedLength
		record.Error = strings.TrimSpace(status.ErrorCode + " " + status.ErrorMessage)
		applied, applyErr := o.Repository.Apply(ctx, snapshot.Links[record.TaskID], record, !exists, time.Now(), o.TTL)
		result = errors.Join(result, applyErr)
		if applied && applyErr == nil && previousStatus != record.Status && o.Logger != nil {
			level := zap.InfoLevel
			if record.Status == aria2StatusError {
				level = zap.ErrorLevel
			}
			o.Logger.Log(level, "Aria2 task state changed", zap.String("component", ID),
				zap.String("gid", gid), zap.String("task_id", record.TaskID),
				zap.String("from", previousStatus), zap.String("to", record.Status),
				zap.Int64("total", record.Total), zap.Int64("completed", record.Completed), zap.String("error", record.Error))
		}
	}
	return statuses, result
}

func (o Observer) Sync(ctx context.Context) error {
	o = o.configured()
	_, err := o.Observe(ctx)
	if err != nil {
		return err
	}
	return o.Repository.Cleanup(ctx, time.Now(), o.TTL)
}

func (o Observer) match(status types.Aria2DownloadStatus, links map[string]ports.ObservedLink) (string, string) {
	base, err := url.Parse(o.PublicBaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return "", ""
	}
	prefix := strings.TrimRight(base.EscapedPath(), "/") + "/download/"
	for _, file := range status.Files {
		for _, uri := range file.URIs {
			u, err := url.Parse(strings.TrimSpace(uri.URI))
			if err != nil || !strings.EqualFold(u.Host, base.Host) || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.HasPrefix(u.EscapedPath(), prefix) {
				continue
			}
			tail := strings.TrimPrefix(u.EscapedPath(), prefix)
			id, err := url.PathUnescape(strings.SplitN(tail, "/", 2)[0])
			if err != nil || id == "" || id == "index" || strings.ContainsAny(id, `/\`) {
				continue
			}
			if _, exists := links[id]; exists {
				return id, uri.URI
			}
		}
	}
	return "", ""
}
