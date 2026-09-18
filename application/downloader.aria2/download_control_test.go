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
