package aria2

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/snakexgc/tdl/application"
	component "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

type Manager struct {
	*component.Manager
	account types.AccountID
	mu      sync.Mutex
	host    *rte.Runtime
	store   *rteconfig.Store
}

func (m *Manager) SetComponentStore(store *rteconfig.Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
}

func NewManager(cfg *config.Config, kvd storage.Storage, logger *zap.Logger, engines ...kv.Storage) *Manager {
	opts := componentOptions(cfg, kvd)
	repository := taskhub.LinkRepository{Store: kvd, Namespace: string(opts.Account)}
	if len(engines) > 0 {
		repository.Engine = engines[0]
	}
	opts.Observations = taskhub.Aria2Observations{Links: repository}
	if cfg != nil {
		opts.LinkTTL = downloadLinkTTL(cfg.HTTP)
	}
	return &Manager{Manager: component.NewManager(opts, logger), account: opts.Account}
}

func (m *Manager) Host() *rte.Runtime {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.host
}

func (m *Manager) Run(ctx context.Context) error {
	finished := make(chan error, 1)
	m.mu.Lock()
	store := m.store
	m.mu.Unlock()
	host, err := application.Aria2DownloadHost(ctx, m.account, m.Manager, finished, store)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.host = host
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.host = nil; m.mu.Unlock() }()
	select {
	case <-ctx.Done():
	case err = <-finished:
	}
	if stopErr := host.Stop(context.Background()); stopErr != nil {
		return stopErr
	}
	return err
}
