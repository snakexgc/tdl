package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

type offlineAria2Client struct{ ports.Aria2Client }

func (offlineAria2Client) SetMaxConcurrentDownloads(context.Context, int) error {
	return errors.New("offline")
}

type unusedAria2Repository struct{ ports.Aria2Repository }

func TestAria2HostLoadsSavedGovernanceBeforeStarting(t *testing.T) {
	ctx := context.Background()
	store := configtest.NewStore()
	view, err := config.New(aria2.Manifest().Config, map[string]any{"error_threshold": 9})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, aria2.ID, true, view))
	newManager := func() *aria2.Manager {
		return aria2.NewManager(aria2.Options{Account: types.DefaultAccount, Client: offlineAria2Client{}, Store: unusedAria2Repository{}, Limit: 1}, nil)
	}
	host, err := Aria2DownloadHost(ctx, types.DefaultAccount, newManager(), nil, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	values := host.Configurations()[0].Values
	require.EqualValues(t, 9, values["error_threshold"])
	require.NoError(t, host.Stop(ctx))
	require.NoError(t, store.Save(ctx, aria2.ID, false, view))
	_, err = Aria2DownloadHost(ctx, types.DefaultAccount, newManager(), nil, store)
	require.ErrorContains(t, err, "disabled")
}
