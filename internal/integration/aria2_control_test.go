package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
)

const (
	testControlledGID   = "controlled"
	testDeleteAction    = "delete"
	testLinkSource      = "source"
	testIsolatedAccount = "isolated"
)

type controlFenceClient struct {
	ports.Aria2Client
	pause  func() error
	status types.Aria2DownloadStatus
	add    func() (string, error)
}

func (c controlFenceClient) TellStatus(context.Context, string) (types.Aria2DownloadStatus, error) {
	if c.status.GID != "" {
		return c.status, nil
	}
	return types.Aria2DownloadStatus{GID: testControlledGID, Status: "active"}, nil
}

func (c controlFenceClient) Pause(context.Context, string) error              { return c.pause() }
func (controlFenceClient) Remove(context.Context, string) error               { return nil }
func (controlFenceClient) RemoveDownloadResult(context.Context, string) error { return nil }
func (c controlFenceClient) AddURI(context.Context, string, types.Aria2AddURIOptions) (string, error) {
	return c.add()
}

func (controlFenceClient) TellActive(context.Context) ([]types.Aria2DownloadStatus, error) {
	return nil, nil
}

func (controlFenceClient) TellWaiting(context.Context, int, int) ([]types.Aria2DownloadStatus, error) {
	return nil, nil
}

func (c controlFenceClient) TellStopped(context.Context, int, int) ([]types.Aria2DownloadStatus, error) {
	return []types.Aria2DownloadStatus{c.status}, nil
}

func TestAria2DeleteAndRetryCannotBeRediscoveredOrRepeated(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := testDeleteAction
		if retry {
			name = "retry"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "store")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			store, err := engine.Open(testIsolatedAccount)
			require.NoError(t, err)
			require.NoError(t, store.Set(ctx, taskhub.LinkPrefix+testLinkSource, []byte(`{"id":"source"}`)))
			repo := taskhub.NewAria2Repository(store)
			const uri = "https://downloads.test/download/source"
			require.NoError(t, repo.Add(ctx, types.Aria2TaskRecord{GID: testControlledGID, TaskID: testLinkSource, Status: "error", DownloadURL: uri}))
			observations := taskhub.Aria2Observations{Links: taskhub.LinkRepository{Store: store, Engine: engine, Namespace: testIsolatedAccount}}
			var controller *aria2.Controller
			calls := 0
			client := controlFenceClient{status: types.Aria2DownloadStatus{GID: testControlledGID, Status: "error", Files: []types.Aria2File{{URIs: []types.Aria2URI{{URI: uri}}}}}, add: func() (string, error) {
				calls++
				concurrent, err := controller.RetryStopped(ctx)
				require.NoError(t, err)
				require.Zero(t, concurrent.Changed)
				require.Len(t, concurrent.Errors, 1)
				return "replacement", nil
			}}
			controller = aria2.NewController(aria2.Options{Client: client, Store: repo}, nil)
			if retry {
				result, err := controller.RetryStopped(ctx)
				require.NoError(t, err)
				require.Empty(t, result.Errors)
				require.Equal(t, 1, result.Changed)
				require.Equal(t, 1, calls)
			} else {
				require.NoError(t, controller.RemoveTask(ctx, testControlledGID))
			}
			// The remote server still returns the pre-control error. A fresh
			// snapshot after deletion must retain the tombstone and reject it.
			observer := aria2.Observer{Client: client, Repository: observations, PublicBaseURL: "https://downloads.test", TTL: time.Hour}
			require.NoError(t, observer.Sync(ctx))
			records, err := repo.Records(ctx)
			require.NoError(t, err)
			require.NotContains(t, records, testControlledGID)
			if retry {
				require.Equal(t, "waiting", records["replacement"].Status)
			} else {
				require.Empty(t, records)
			}
			snapshot, err := observations.Snapshot(ctx)
			require.NoError(t, err)
			require.True(t, snapshot.Records[testControlledGID].Deleted)
			record := snapshot.Records[testControlledGID]
			record.Status = string(types.DownloadComplete)
			applied, err := observations.Apply(ctx, snapshot.Links[testLinkSource], record, false, time.Now(), time.Hour)
			require.NoError(t, err)
			require.False(t, applied)
			result, err := controller.RetryStopped(ctx)
			require.NoError(t, err)
			require.Zero(t, result.Changed)
		})
	}
}

func TestAria2ControlFencesObservationsBeforeDuringAndAfterRPC(t *testing.T) {
	for _, rpcFails := range []bool{false, true} {
		name := "success"
		if rpcFails {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "store")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			store, err := engine.Open(testIsolatedAccount)
			require.NoError(t, err)
			require.NoError(t, store.Set(ctx, taskhub.LinkPrefix+testLinkSource, []byte(`{"id":"source"}`)))
			repo := taskhub.NewAria2Repository(store)
			require.NoError(t, repo.Add(ctx, types.Aria2TaskRecord{GID: testControlledGID, TaskID: testLinkSource, Status: "active"}))
			observations := taskhub.Aria2Observations{Links: taskhub.LinkRepository{Store: store, Engine: engine, Namespace: testIsolatedAccount}}
			before, err := observations.Snapshot(ctx)
			require.NoError(t, err)
			var during ports.Aria2ObservationSnapshot
			client := controlFenceClient{pause: func() error {
				during, err = observations.Snapshot(ctx)
				require.NoError(t, err)
				record := during.Records[testControlledGID]
				require.True(t, record.ControlUntil.After(time.Now()))
				record.Status = string(types.DownloadComplete)
				applied, applyErr := observations.Apply(ctx, during.Links[testLinkSource], record, false, time.Now(), time.Hour)
				require.NoError(t, applyErr)
				require.False(t, applied)
				applied, applyErr = repo.Report(ctx, record, record.Revision)
				require.NoError(t, applyErr)
				require.False(t, applied)
				_, reserved, reserveErr := repo.ReserveControl(ctx, record, time.Now().Add(time.Minute))
				require.NoError(t, reserveErr)
				require.False(t, reserved)
				if rpcFails {
					return errors.New("connection lost")
				}
				return nil
			}}
			controller := aria2.NewController(aria2.Options{Client: client, Store: repo}, nil)
			err = controller.PauseTask(ctx, testControlledGID)
			if rpcFails {
				require.ErrorContains(t, err, "connection lost")
			} else {
				require.NoError(t, err)
			}
			for _, snapshot := range []ports.Aria2ObservationSnapshot{before, during} {
				record := snapshot.Records[testControlledGID]
				record.Status = string(types.DownloadComplete)
				applied, err := observations.Apply(ctx, snapshot.Links[testLinkSource], record, false, time.Now(), time.Hour)
				require.NoError(t, err)
				require.False(t, applied)
			}
			after, err := observations.Snapshot(ctx)
			require.NoError(t, err)
			record := after.Records[testControlledGID]
			require.True(t, record.ControlUntil.IsZero())
			require.Greater(t, record.Revision, during.Records[testControlledGID].Revision)
			if rpcFails {
				require.Equal(t, "active", record.Status)
			} else {
				require.Equal(t, "paused", record.Status)
			}
			require.False(t, after.Links[testLinkSource].Task.Downloaded)
			// Simulate a caller that crashed while its RPC lease was held.
			orphan, reserved, err := repo.ReserveControl(ctx, record, time.Now().Add(time.Minute))
			require.NoError(t, err)
			require.True(t, reserved)
			expired := orphan
			expired.ControlUntil = time.Now().Add(-time.Second)
			data, err := json.Marshal(expired)
			require.NoError(t, err)
			require.NoError(t, store.Set(ctx, taskhub.Aria2StorageKey(expired.GID), data))
			recovered, reserved, err := repo.ReserveControl(ctx, expired, time.Now().Add(time.Minute))
			require.NoError(t, err)
			require.True(t, reserved)
			applied, err := repo.FinishControl(ctx, orphan, "", true)
			require.NoError(t, err)
			require.False(t, applied, "the crashed caller cannot delete its replacement lease")
			applied, err = repo.FinishControl(ctx, recovered, "", false)
			require.NoError(t, err)
			require.True(t, applied)
		})
	}
}

func TestManualPauseClearsPersistedAutomaticPauseOwner(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "store")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open(testIsolatedAccount)
	require.NoError(t, err)
	repo := taskhub.NewAria2Repository(store)
	require.NoError(t, repo.Add(ctx, types.Aria2TaskRecord{GID: testControlledGID, TaskID: testLinkSource, Status: "paused", PauseOwner: "automatic"}))
	controller := aria2.NewController(aria2.Options{Client: controlFenceClient{status: types.Aria2DownloadStatus{GID: testControlledGID, Status: "paused"}}, Store: repo}, nil)
	require.NoError(t, controller.PauseTask(ctx, testControlledGID))
	records, err := repo.Records(ctx)
	require.NoError(t, err)
	require.Equal(t, "paused", records[testControlledGID].Status)
	require.Empty(t, records[testControlledGID].PauseOwner)
}
