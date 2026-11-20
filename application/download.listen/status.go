package downloadlisten

import (
	"context"
	"net/http"
	"sort"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

func Routes() []types.WebRoute {
	return []types.WebRoute{{Path: "/api/listen-downloads/status", Owner: ID, Port: ports.DownloadListenStatusName}}
}

func (s *Service) Handle(ctx context.Context, request types.WebRequest) (types.WebResponse, error) {
	if err := ctx.Err(); err != nil {
		return types.WebResponse{}, err
	}
	if request.Method != http.MethodGet {
		return types.WebResponse{Status: http.StatusMethodNotAllowed, Body: map[string]any{"error": "只支持 GET"}}, nil
	}
	return types.WebResponse{Body: s.Status()}, nil
}

func (s *Service) Status() types.ListenStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := types.ListenStatus{Connected: s.connected, DownloadEnabled: s.enabled != nil && s.enabled(), Sources: []types.ListenSourceRecord{}, Tasks: []types.ListenTask{}, ScanIntervalSeconds: int(scanInterval.Seconds()), LastError: s.lastError, Filters: s.filters}
	if s.current == nil {
		return status
	}
	for ref := range s.current.record.Starts {
		source := s.sourceLocked(ref)
		source.Info.Peer.AccessHash, source.Info.Discussion.AccessHash = 0, 0
		status.Sources = append(status.Sources, source)
	}
	sort.Slice(status.Sources, func(i, j int) bool { return status.Sources[i].Ref < status.Sources[j].Ref })
	for _, job := range s.state.Jobs {
		if job.Source != "" {
			status.Tasks = append(status.Tasks, types.ListenTask{ID: job.ID, Source: job.Source, TaskID: job.Message.MediaID, MessageID: job.Message.ID, Date: job.Message.Date, FileName: job.Message.FileName, Total: job.Message.FileSize, Comment: job.Comment, State: job.State, Error: job.LastError})
		}
		if job.State == statePending || job.State == stateSubmitting {
			status.Pending++
		}
		if job.State == stateUncertain {
			status.Uncertain++
		}
	}
	sort.Slice(status.Tasks, func(i, j int) bool {
		if status.Tasks[i].Date != status.Tasks[j].Date {
			return status.Tasks[i].Date > status.Tasks[j].Date
		}
		return status.Tasks[i].ID < status.Tasks[j].ID
	})
	for _, thread := range s.state.Threads {
		if s.clock().Before(thread.ExpiresAt) && s.current.comments[thread.Source] && thread.PostDate >= s.current.record.Starts[thread.Source] {
			status.Tracking++
		}
	}
	return status
}
