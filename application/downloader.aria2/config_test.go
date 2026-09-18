package aria2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const testGovernanceBaseURL = "http://localhost:8080"

type policyClient struct {
	fakeAria2ControlClient
	attempts       chan struct{}
	queries        chan struct{}
	monitorQueries chan struct{}
	online         bool
}

func (c *policyClient) SetMaxConcurrentDownloads(context.Context, int) error {
	select {
	case c.attempts <- struct{}{}:
	default:
	}
	if c.online {
		return nil
	}
	return errors.New("offline")
}

func (c *policyClient) TellActive(ctx context.Context) ([]DownloadStatus, error) {
	if _, bounded := ctx.Deadline(); bounded {
		select {
		case c.monitorQueries <- struct{}{}:
		default:
		}
	}
	select {
	case c.queries <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestMonitorAdoptsHotIntervalWithoutRestart(t *testing.T) {
	client := &policyClient{online: true, attempts: make(chan struct{}, 1), monitorQueries: make(chan struct{}, 1)}
	manager := NewManager(Options{Account: testAccount, Client: client, Store: newTestRepository(), PublicBaseURL: testGovernanceBaseURL, Limit: 1}, nil)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, manager, nil))
	host, err := registry.Build(testAccount, nil, nil)
	require.NoError(t, err)
	ctx := context.Background()
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	select {
	case <-client.attempts:
	case <-time.After(time.Second):
		t.Fatal("manager did not connect")
	}
	require.NoError(t, host.PatchSaved(ctx, ID, map[string]any{monitorPollField: 100}, config.NewStore(t.TempDir())))
	select {
	case <-client.monitorQueries:
	case <-time.After(time.Second):
		t.Fatal("monitor retained the old interval")
	}
}

func TestGovernanceConfigPersistsAndWakesActiveSchedules(t *testing.T) {
	ctx := context.Background()
	client := &policyClient{attempts: make(chan struct{}, 10), queries: make(chan struct{}, 10)}
	manager := NewManager(Options{Account: testAccount, Client: client, Store: newTestRepository(), PublicBaseURL: testGovernanceBaseURL, Limit: 1}, nil)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, manager, nil))
	host, err := registry.Build(testAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	select {
	case <-client.attempts:
	case <-time.After(time.Second):
		t.Fatal("connection attempt did not start")
	}
	store := config.NewStore(t.TempDir())
	before := manager.policy()
	require.Error(t, host.PatchSaved(ctx, ID, map[string]any{"connect_retry_ms": 70000}, store))
	require.Equal(t, before, manager.policy())
	patch := map[string]any{"connect_retry_ms": 100, "connect_retry_max_ms": 200, "status_interval_ms": 100, monitorPollField: 200, "monitor_stall_seconds": 4, "error_threshold": 7}
	require.NoError(t, host.PatchSaved(ctx, ID, patch, store))
	select {
	case <-client.attempts:
	case <-time.After(time.Second):
		t.Fatal("retry did not wake after configuration update")
	}
	select {
	case <-client.queries:
	case <-time.After(time.Second):
		t.Fatal("status sync did not adopt updated interval")
	}
	require.Equal(t, 200*time.Millisecond, manager.monitor.settings().PollInterval)
	require.Equal(t, 4*time.Second, manager.monitor.settings().StallThreshold)
	require.Equal(t, 7, manager.regulator.settings().Threshold)
	document, err := store.Load(ctx, ID)
	require.NoError(t, err)
	require.NoError(t, host.Stop(ctx))
	other := NewManager(Options{Account: testAccount, Client: client, Store: newTestRepository(), PublicBaseURL: testGovernanceBaseURL, Limit: 1}, nil)
	registry = rte.NewRegistry()
	require.NoError(t, Register(registry, other, nil))
	restarted, err := registry.Build(testAccount, nil, map[string]map[string]any{ID: document.Values})
	require.NoError(t, err)
	require.Equal(t, rte.Running, restarted.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, restarted.Stop(ctx)) })
	require.Equal(t, manager.policy(), other.policy())
}
