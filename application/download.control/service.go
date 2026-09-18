package downloadcontrol

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	statusWaiting  = "waiting"
	actionResume   = "resume"
	actionDelete   = "delete"
	statusError    = "error"
	statusActive   = "active"
	statusPaused   = "paused"
	statusQueued   = "queued"
	statusComplete = "complete"
	localExecutor  = "local"
)

type Service struct {
	account  types.AccountID
	backends map[string]ports.DownloadBackend
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	active   sync.WaitGroup
	route    atomic.Pointer[ports.DownloadRoute]
}

func New(account types.AccountID, backends map[string]ports.DownloadBackend) *Service {
	if account == "" {
		account = types.DefaultAccount
	}
	s := &Service{account: account, backends: make(map[string]ports.DownloadBackend, len(backends))}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	for name, backend := range backends {
		s.backends[name] = backend
	}
	return s
}

func (s *Service) backend(ctx context.Context, account types.AccountID, executor string) (ports.DownloadBackend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if account != s.account {
		return nil, fmt.Errorf("download account mismatch")
	}
	backend := s.backends[executor]
	if backend == nil {
		return nil, fmt.Errorf("download executor %q is unavailable", executor)
	}
	return backend, nil
}

func (s *Service) Tasks(ctx context.Context, account types.AccountID, executor string) ([]types.DownloadTask, error) {
	ctx, done, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	backend, err := s.backend(ctx, account, executor)
	if err != nil {
		return nil, err
	}
	items, err := backend.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	items = append([]types.DownloadTask{}, items...)
	for i := range items {
		items[i].Account = account
		items[i].Executor = executor
		items[i].State = types.NormalizeDownloadState(items[i].Status)
		if items[i].StartedAt != nil {
			started := *items[i].StartedAt
			items[i].StartedAt = &started
		}
	}
	return items, nil
}

func normalizedState(status string) types.DownloadState {
	return types.NormalizeDownloadState(status)
}

func (s *Service) Control(ctx context.Context, request types.DownloadAction) (types.DownloadActionResult, error) {
	var result types.DownloadActionResult
	ctx, done, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer done()
	backend, err := s.backend(ctx, request.Account, request.Executor)
	if err != nil {
		return result, err
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	all := strings.HasSuffix(action, "_all")
	action = strings.TrimSuffix(action, "_all")
	if action == "start" {
		action = actionResume
	}
	if action != "pause" && action != actionResume && action != actionDelete {
		return result, fmt.Errorf("unsupported download action %q", request.Action)
	}
	ids := request.IDs
	if all {
		items, err := backend.ListTasks(ctx)
		if err != nil {
			return result, err
		}
		statuses := append([]string{}, request.Statuses...)
		for i := range statuses {
			statuses[i] = strings.ToLower(strings.TrimSpace(statuses[i]))
		}
		if action == actionDelete && len(statuses) == 0 {
			statuses = []string{statusComplete, statusError}
		}
		ids = nil
		for _, task := range items {
			eligible := action == actionDelete && slices.Contains(statuses, task.Status) ||
				action == "pause" && slices.Contains([]string{statusActive, statusQueued, statusWaiting, statusError}, task.Status) ||
				action == actionResume && slices.Contains([]string{statusPaused, statusError}, task.Status)
			if eligible {
				ids = append(ids, task.ID)
			}
		}
	}
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return result, nil
	}
	return backend.ChangeTasks(ctx, action, unique)
}
