package aria2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type liveLimitClient struct {
	fakeAria2ControlClient
	limits chan int
}

func (c *liveLimitClient) SetMaxConcurrentDownloads(ctx context.Context, limit int) error {
	select {
	case c.limits <- limit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestManagerAppliesLiveLimitsWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &liveLimitClient{limits: make(chan int, 4)}
	manager := NewManager(Options{Account: testAccount, Client: client, Store: newTestRepository(), Connections: 1, Limit: 1}, nil)
	finished := make(chan error, 1)
	go func() { finished <- manager.Run(ctx) }()
	select {
	case limit := <-client.limits:
		require.Equal(t, 1, limit)
	case <-time.After(time.Second):
		t.Fatal("manager did not connect")
	}
	manager.UpdateTransferLimits(5, 8)
	select {
	case limit := <-client.limits:
		require.Equal(t, 5, limit)
	case <-time.After(time.Second):
		t.Fatal("limit was not applied")
	}
	_, err := manager.Submit(ctx, types.DownloadSubmission{Account: testAccount, TaskID: "live", DownloadURL: "http://localhost/download/live"})
	require.NoError(t, err)
	require.Equal(t, 8, client.addedOptions[0].Connections)
	cancel()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("manager did not stop")
	}
}
