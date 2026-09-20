package aria2

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadControlDoesNotOwnTasksByURLAlone(t *testing.T) {
	const ownedID = "owned-id"
	ctx := context.Background()
	store := newTestRepository()
	require.NoError(t, store.Add(ctx, TaskRecord{GID: ownedID, TaskID: testDocument1}))
	client := &fakeAria2ControlClient{active: []DownloadStatus{
		{GID: ownedID, Status: aria2StatusActive},
		{GID: "unregistered-id", Status: aria2StatusActive, Files: filesWithURI(testDownloadURL1)},
	}}
	c := &Controller{store: store, client: client, publicBaseURL: strings.TrimSuffix(testDownloadURL1, "/download/"+testDocument1)}
	items, err := c.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, ownedID, items[0].ID)
	result, err := c.ChangeTasks(ctx, "pause", []string{"unregistered-id", ownedID})
	require.NoError(t, err)
	require.Equal(t, 1, result.Changed)
	require.Equal(t, []string{ownedID}, client.paused)
}

func TestTaskControlsRejectUnregisteredTasks(t *testing.T) {
	ctx := context.Background()
	client := &fakeAria2ControlClient{
		active:  []DownloadStatus{{GID: "foreign-active", Status: aria2StatusActive, Files: filesWithURI(testDownloadURL1)}},
		waiting: []DownloadStatus{{GID: "foreign-paused", Status: aria2StatusPaused, Files: filesWithURI(testDownloadURL1)}},
		stopped: []DownloadStatus{{GID: "foreign-complete", Status: aria2StatusComplete, Files: filesWithURI(testDownloadURL1)}},
	}
	c := &Controller{store: newTestRepository(), client: client, publicBaseURL: testControlBaseURL}
	require.Error(t, c.PauseTask(ctx, "foreign-active"))
	require.Error(t, c.UnpauseTask(ctx, "foreign-paused"))
	require.Error(t, c.RemoveTask(ctx, "foreign-active"))
	for _, operation := range []func(context.Context) (ActionResult, error){c.PauseAll, c.StartAll, c.ClearStopped} {
		result, err := operation(ctx)
		require.NoError(t, err)
		require.Zero(t, result.Changed)
	}
	for _, list := range []func(context.Context) ([]DownloadStatus, error){c.ActiveTasks, c.WaitingTasks, c.StoppedTasks} {
		items, err := list(ctx)
		require.NoError(t, err)
		require.Empty(t, items)
	}
	require.Empty(t, client.paused)
	require.Empty(t, client.forcePaused)
	require.Empty(t, client.unpaused)
	require.Empty(t, client.removed)
	require.Empty(t, client.removedResults)
}
