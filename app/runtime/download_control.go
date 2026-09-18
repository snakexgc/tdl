package runtime

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/pkg/config"
)

func (m *Manager) initDownloadControl(ctx context.Context) {
	var err error
	m.downloadHost, m.downloadPort, err = application.DownloadControlHost(ctx, m.downloadAccount, map[string]ports.DownloadBackend{
		"local": watch.NewInternalDownloadController(m.namespaceKV),
		"aria2": aria2ControlBackend{manager: m},
	}, m.componentStore)
	if err != nil {
		logctx.From(ctx).Error("start download control", zap.Error(err))
	}
}

func (m *Manager) Route(ctx context.Context, account types.AccountID) (ports.DownloadRoute, error) {
	if m.downloadHost == nil {
		return ports.DownloadRoute{}, fmt.Errorf("download routing is unavailable")
	}
	value, err := m.downloadHost.Resolve(ports.DownloadRoutingName)
	if err != nil {
		return ports.DownloadRoute{}, err
	}
	return value.(ports.DownloadRouting).Route(ctx, account)
}

type aria2ControlBackend struct{ manager *Manager }

func (b aria2ControlBackend) ListTasks(ctx context.Context) ([]types.DownloadTask, error) {
	return aria2.NewController(config.Get(), b.manager.namespaceKV, nil).ListTasks(ctx)
}

func (b aria2ControlBackend) ChangeTasks(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	return aria2.NewController(config.Get(), b.manager.namespaceKV, nil).ChangeTasks(ctx, action, ids)
}

func (m *Manager) Tasks(ctx context.Context, account types.AccountID, executor string) ([]types.DownloadTask, error) {
	if m.downloadPort == nil {
		return nil, fmt.Errorf("download control is unavailable")
	}
	return m.downloadPort.Tasks(ctx, account, executor)
}

func (m *Manager) Control(ctx context.Context, request types.DownloadAction) (types.DownloadActionResult, error) {
	if m.downloadPort == nil {
		return types.DownloadActionResult{}, fmt.Errorf("download control is unavailable")
	}
	return m.downloadPort.Control(ctx, request)
}
