package maintenance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "storage.maintenance"

func Register(registry *rte.Registry, repository ports.CleanupRepository) error {
	if repository == nil {
		return errors.New("cleanup repository is required")
	}
	return registry.Register(Manifest(), func() rte.Component { return &Service{repository: repository} })
}

func Manifest() manifest.Manifest {
	return manifest.Manifest{
		Feature: manifest.Feature{ID: "panel", Title: "面板与数据维护", Order: 70, SettingsURL: "/config?tab=panel"}, ID: ID, Commands: Commands(), Title: "存储维护", Provides: []manifest.Port{manifest.PortOf[ports.KVMaintenance](ports.KVMaintenanceName, 1, 0)},
	}
}

type Service struct {
	repository ports.CleanupRepository
	account    types.AccountID
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	active     sync.WaitGroup
}

func (s *Service) Init(ctx context.Context, k rte.Kernel) error {
	s.account = k.Account
	s.ctx, s.cancel = context.WithCancel(ctx)
	return k.Provide(ports.KVMaintenanceName, s)
}
func (*Service) Start(context.Context) error                    { return nil }
func (*Service) Reconfigure(context.Context, config.View) error { return nil }
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.active.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Clean(ctx context.Context, account types.AccountID) (ports.CleanupResult, error) {
	result := ports.CleanupResult{Namespace: string(account)}
	if account != s.account {
		return result, errors.New("cleanup account mismatch")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return result, errors.New("storage maintenance is stopped")
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	call, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(s.ctx, cancel)
	defer func() { unlink(); cancel() }()
	snapshot, err := s.repository.Snapshot(call)
	if err != nil {
		return result, err
	}
	result.Kept = snapshot.Protected
	keys := make([]string, 0, len(snapshot.Records))
	for key := range snapshot.Records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := call.Err(); err != nil {
			return result, err
		}
		deleted, err := s.repository.DeleteUnchanged(call, key, snapshot.Records[key])
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		if deleted {
			result.Deleted++
		} else {
			result.Kept++
		}
	}
	return result, call.Err()
}
