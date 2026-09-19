package aria2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type linkObservation struct {
	applied []types.Aria2TaskRecord
	ttls    []time.Duration
}

func (*linkObservation) Snapshot(context.Context) (ports.Aria2ObservationSnapshot, error) {
	return ports.Aria2ObservationSnapshot{Links: map[string]ports.ObservedLink{testDocument1: {}}}, nil
}

func (o *linkObservation) Apply(_ context.Context, _ ports.ObservedLink, record types.Aria2TaskRecord, _ bool, _ time.Time, ttl time.Duration) (bool, error) {
	o.applied, o.ttls = append(o.applied, record), append(o.ttls, ttl)
	return true, nil
}

func (o *linkObservation) Cleanup(_ context.Context, _ time.Time, ttl time.Duration) error {
	o.ttls = append(o.ttls, ttl)
	return nil
}

func TestLinkPolicyUpdatesOwnershipAndRetentionWithoutTransferActions(t *testing.T) {
	client := &liveLimitClient{fakeAria2ControlClient: fakeAria2ControlClient{active: []aria2DownloadStatus{{
		GID: testGIDNew, Status: aria2StatusActive,
		Files: filesWithURI("https://new.example/download/" + testDocument1),
	}}}}
	observations := &linkObservation{}
	manager := NewManager(Options{Client: client, Store: newTestRepository(), Observations: observations, PublicBaseURL: "https://old.example", LinkTTL: time.Hour}, nil)
	ctx := context.Background()
	before, err := manager.controller.Overview(ctx)
	require.NoError(t, err)
	require.Zero(t, before.TotalTasks)
	manager.UpdateLinkPolicy("https://new.example", 3*time.Hour)
	after, err := manager.controller.Overview(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, after.TotalTasks)
	require.NoError(t, manager.syncStates(ctx))
	require.Len(t, observations.applied, 1)
	require.Equal(t, testDocument1, observations.applied[0].TaskID)
	require.Equal(t, []time.Duration{3 * time.Hour, 3 * time.Hour}, observations.ttls)
	require.Empty(t, client.forcePaused)
	require.Empty(t, client.unpaused)
}
