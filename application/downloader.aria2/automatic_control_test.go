package aria2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type automaticTestClient struct{ *fakeAria2ControlClient }

func (automaticTestClient) SetMaxConcurrentDownloads(context.Context, int) error { return nil }

func (c automaticTestClient) ForcePause(ctx context.Context, gid string) error {
	if err := c.fakeAria2ControlClient.ForcePause(ctx, gid); err != nil {
		return err
	}
	for i := range c.active {
		if c.active[i].GID == gid {
			c.active[i].Status = aria2StatusPaused
		}
	}
	return nil
}

func (c automaticTestClient) Unpause(ctx context.Context, gid string) error {
	if err := c.fakeAria2ControlClient.Unpause(ctx, gid); err != nil {
		return err
	}
	for i := range c.active {
		if c.active[i].GID == gid {
			c.active[i].Status = aria2StatusActive
		}
	}
	return nil
}

func TestAutomaticResumePreservesManualPauseAcrossObserversAndRestart(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepository()
	const gid = "controlled"
	require.NoError(t, repo.Add(ctx, TaskRecord{GID: gid, TaskID: testDocument1, Status: aria2StatusActive}))
	client := automaticTestClient{&fakeAria2ControlClient{active: []DownloadStatus{{GID: gid, Status: aria2StatusActive}}}}
	manager := NewManager(Options{Client: client, Store: repo}, nil)
	require.NoError(t, manager.regulator.client.ForcePause(ctx, gid))
	records, err := repo.Records(ctx)
	require.NoError(t, err)
	paused := records[gid]
	require.NotEmpty(t, paused.PauseOwner)
	applied, err := repo.Report(ctx, paused, paused.Revision)
	require.NoError(t, err)
	require.True(t, applied)
	require.ErrorIs(t, manager.monitor.client.Unpause(ctx, gid), errAutomaticControlSkipped)
	require.NoError(t, manager.regulator.client.Unpause(ctx, gid), "an observation must not steal pause ownership")
	require.Len(t, client.unpaused, 1)
	require.NoError(t, manager.regulator.client.ForcePause(ctx, gid))
	require.NoError(t, manager.controller.PauseTask(ctx, gid), "manual pause claims an already paused task")
	records, err = repo.Records(ctx)
	require.NoError(t, err)
	require.Empty(t, records[gid].PauseOwner)
	require.ErrorIs(t, manager.regulator.client.Unpause(ctx, gid), errAutomaticControlSkipped)
	restarted := NewManager(Options{Client: client, Store: repo}, nil)
	require.ErrorIs(t, restarted.automatic.Unpause(ctx, gid), errAutomaticControlSkipped)
	require.Len(t, client.unpaused, 1)
	require.ErrorIs(t, manager.automatic.ForcePause(ctx, "foreign"), errAutomaticControlSkipped)
	require.Len(t, client.forcePaused, 2)
}
