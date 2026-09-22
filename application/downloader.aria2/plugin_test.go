package aria2

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type managerClient struct {
	fakeAria2ControlClient
	ready chan struct{}
}

type canceledStartupClient struct {
	managerClient
	cancel context.CancelFunc
}

func (c *canceledStartupClient) TellWaiting(ctx context.Context, _, _ int) ([]DownloadStatus, error) {
	c.cancel()
	return nil, ctx.Err()
}

func TestManagerCancellationDuringStartupRecoveryIsNormalShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &canceledStartupClient{managerClient: managerClient{ready: make(chan struct{})}, cancel: cancel}
	manager := NewManager(Options{Client: client, Store: newTestRepository(), PublicBaseURL: testGovernanceBaseURL, Limit: 1}, nil)
	require.NoError(t, manager.Run(ctx))
}

type blockingMonitorClient struct {
	managerClient
	once                       sync.Once
	entered, canceled, release chan struct{}
}

func (c *blockingMonitorClient) TellActive(ctx context.Context) ([]DownloadStatus, error) {
	select {
	case <-c.release:
		return nil, nil
	default:
	}
	// Startup recovery has no deadline; monitor requests do.
	if _, bounded := ctx.Deadline(); !bounded {
		return nil, nil
	}
	c.once.Do(func() { close(c.entered) })
	<-ctx.Done()
	close(c.canceled)
	<-c.release
	return nil, ctx.Err()
}

func TestStopRetainsGovernorUntilTransportReturns(t *testing.T) {
	client := &blockingMonitorClient{
		managerClient: managerClient{ready: make(chan struct{})},
		entered:       make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}),
	}
	manager := NewManager(Options{Account: testAccount, Client: client, Store: newTestRepository(), PublicBaseURL: testGovernanceBaseURL, Connections: 1, Limit: 1}, nil)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, manager, nil))
	host, err := registry.Build(testAccount, nil, map[string]map[string]any{ID: {monitorPollField: 100}})
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	defer func() { close(client.release); _ = host.Stop(context.Background()) }()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("monitor did not enter transport")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	select {
	case <-client.canceled:
	case <-time.After(time.Second):
		t.Fatal("monitor request was not canceled")
	}
}

func (c *managerClient) SetMaxConcurrentDownloads(context.Context, int) error {
	close(c.ready)
	return nil
}

func TestAria2ComponentProvidesRealPortsAndStopsManager(t *testing.T) {
	registry := rte.NewRegistry()
	require.Error(t, Register(registry, nil, nil))
	client := &managerClient{ready: make(chan struct{})}
	manager := NewManager(Options{Account: testAccount, Client: client, Store: newTestRepository(), PublicBaseURL: testGovernanceBaseURL, Connections: 1, Limit: 1}, nil)
	finished := make(chan error, 1)
	require.NoError(t, Register(registry, manager, finished))
	host, err := registry.Build(testAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	value, err := host.Resolve(ports.Aria2TasksName)
	require.NoError(t, err)
	_, ok := value.(ports.Aria2Tasks)
	require.True(t, ok)
	value, err = host.Resolve(ports.DownloadExecutorName)
	require.NoError(t, err)
	_, err = value.(ports.DownloadExecutor).Submit(context.Background(), types.DownloadSubmission{Account: "bob", DownloadURL: testDownloadURL1})
	require.ErrorContains(t, err, "account mismatch")
	select {
	case <-client.ready:
	case <-time.After(time.Second):
		t.Fatal("manager did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, host.Stop(ctx))
	select {
	case err := <-finished:
		require.NoError(t, err)
	default:
		t.Fatal("manager still running after stop")
	}
}
