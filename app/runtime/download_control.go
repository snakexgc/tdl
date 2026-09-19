package runtime

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

func (m *Manager) initDownloadControl(ctx context.Context) error {
	host, port, err := application.DownloadControlHost(ctx, m.downloadAccount, map[string]ports.DownloadBackend{
		"local":       watch.NewInternalDownloadController(m.namespaceKV),
		moduleIDAria2: aria2ControlBackend{manager: m},
	}, m.componentStore)
	m.mu.Lock()
	m.downloadHost, m.downloadPort = host, port
	m.mu.Unlock()
	return err
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
	route, err := value.(ports.DownloadRouting).Route(ctx, account)
	if m.componentStore == nil {
		route.Mode = config.EffectiveDownloaderMode(config.From(m.parent))
	}
	return route, err
}

type aria2ControlBackend struct{ manager *Manager }

func (b aria2ControlBackend) ListTasks(ctx context.Context) ([]types.DownloadTask, error) {
	return aria2.NewController(config.From(b.manager.parent), b.manager.namespaceKV, nil).ListTasks(ctx)
}

func (b aria2ControlBackend) ChangeTasks(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	return aria2.NewController(config.From(b.manager.parent), b.manager.namespaceKV, nil).ChangeTasks(ctx, action, ids)
}

func (m *Manager) Tasks(ctx context.Context, account types.AccountID, executor string) ([]types.DownloadTask, error) {
	m.mu.Lock()
	port := m.downloadPort
	m.mu.Unlock()
	if port == nil {
		return nil, fmt.Errorf("download control is unavailable")
	}
	return port.Tasks(ctx, account, executor)
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
	if cfg == nil || !cfg.Modules.Aria2 || !cfg.Aria2.AutoDownload || manager == nil || !m.aria2Process.Running() {
		return types.DownloadResult{}, ports.ErrDownloadNotAccepted
	}
	return manager.Submit(ctx, request)
}
