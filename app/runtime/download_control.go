package runtime

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/app/aria2"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/application"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

const localExecutorID = "local"

func (m *Manager) initDownloadControl(ctx context.Context) error {
	host, port, err := application.DownloadControlHost(ctx, m.downloadAccount, map[string]ports.DownloadBackend{
		localExecutorID: local.NewController(taskhub.NewLocalRepository(m.namespaceKV)),
		moduleIDAria2:   aria2ControlBackend{manager: m},
	}, m.componentStore)
	m.mu.Lock()
	m.downloadHost, m.downloadPort = host, port
	m.mu.Unlock()
	return err
}

type savedLocalLinks struct{ manager *Manager }

func (savedLocalLinks) Name() string { return localExecutorID }
func (s savedLocalLinks) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	m := s.manager
	if !m.componentEnabled(local.ID) {
		return types.DownloadResult{}, fmt.Errorf("local downloader is disabled")
	}
	route, err := m.Route(ctx, in.Account)
	if err != nil {
		return types.DownloadResult{}, err
	}
	naming, err := m.componentPort(ports.NamingRulesName)
	if err != nil {
		return types.DownloadResult{}, err
	}
	return (local.SavedLinks{Account: m.downloadAccount, Root: route.LocalRoot, Naming: naming.(ports.NamingRules), Source: httpdl.NewTaskStore(m.namespaceKV, 0), Repository: taskhub.NewLocalRepository(m.namespaceKV)}).Submit(ctx, in)
}

func (m *Manager) Route(ctx context.Context, account types.AccountID) (ports.DownloadRoute, error) {
	m.mu.Lock()
	host := m.downloadHost
	m.mu.Unlock()
	if host == nil {
		return ports.DownloadRoute{}, fmt.Errorf("download routing is unavailable")
	}
	value, err := host.Resolve(ports.DownloadRoutingName)
	if err != nil {
		return ports.DownloadRoute{}, err
	}
	return value.(ports.DownloadRouting).Route(ctx, account)
}

func (m *Manager) SubmitBatch(ctx context.Context, request types.DownloadIntent, resources ports.DownloadResources) (types.DownloadSubmissionSummary, error) {
	m.mu.Lock()
	host := m.downloadHost
	m.mu.Unlock()
	if host == nil {
		return types.DownloadSubmissionSummary{}, fmt.Errorf("download pipeline is unavailable")
	}
	value, err := host.Resolve(ports.DownloadPipelineName)
	if err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	return value.(ports.DownloadPipeline).SubmitBatch(ctx, request, resources)
}

type aria2ControlBackend struct{ manager *Manager }

func (b aria2ControlBackend) ListTasks(ctx context.Context) ([]types.DownloadTask, error) {
	if !config.Aria2Enabled(config.From(b.manager.parent)) {
		return []types.DownloadTask{}, nil
	}
	return aria2.NewController(config.From(b.manager.parent), b.manager.namespaceKV, nil).ListTasks(ctx)
}

func (b aria2ControlBackend) ChangeTasks(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	if !config.Aria2Enabled(config.From(b.manager.parent)) {
		return types.DownloadActionResult{}, fmt.Errorf("aria2 is not the active downloader")
	}
	return aria2.NewController(config.From(b.manager.parent), b.manager.namespaceKV, nil).ChangeTasks(ctx, action, ids)
}

func (m *Manager) Tasks(ctx context.Context, executor string) ([]types.DownloadTask, error) {
	m.mu.Lock()
	port := m.downloadPort
	m.mu.Unlock()
	if port == nil {
		return nil, fmt.Errorf("download control is unavailable")
	}
	return port.Tasks(ctx, executor)
}

func (m *Manager) Control(ctx context.Context, request types.DownloadAction) (types.DownloadActionResult, error) {
	m.mu.Lock()
	port := m.downloadPort
	m.mu.Unlock()
	if port == nil {
		return types.DownloadActionResult{}, fmt.Errorf("download control is unavailable")
	}
	return port.Control(ctx, request)
}

// Resolve the current executor for each submission. Disabling or reconnecting
// aria2 does not invalidate the Telegram watcher or the HTTP byte stream.
type aria2Submission struct{ manager *Manager }

func (aria2Submission) Name() string { return "aria2" }
func (p aria2Submission) Submit(ctx context.Context, request types.DownloadSubmission) (types.DownloadResult, error) {
	m := p.manager
	cfg := config.From(m.parent)
	m.mu.Lock()
	manager := m.aria2Mgr
	m.mu.Unlock()
	if !config.Aria2Enabled(cfg) || !cfg.Aria2.AutoDownload || manager == nil {
		return types.DownloadResult{}, ports.ErrDownloadNotAccepted
	}
	return manager.Submit(ctx, request)
}
