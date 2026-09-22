package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestWatchDownloadProtocolFeedsOwnedPipelineAndBoltQueue(t *testing.T) {
	for _, skipSame := range []bool{false, true} {
		t.Run(fmt.Sprint("skip_same_", skipSame), func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Downloader.Executors, cfg.Downloader.LocalRoot = []string{localExecutorName}, t.TempDir()
			cfg.Aria2.Dir = filepath.Join(t.TempDir(), "remote-only")
			ctx := config.WithSource(context.Background(), config.NewSource(cfg))
			engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "state"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			storage, err := engine.Open(string(types.DefaultAccount))
			require.NoError(t, err)
			w := namingWatcher(t, "F", 255)
			w.opts.Account, w.opts.Download, w.opts.SkipSame = types.DefaultAccount, true, skipSame
			w.opts.DownloadRouting = &fixedDownloadRoute{ports.DownloadRoute{Executors: cfg.Downloader.Executors, LocalRoot: cfg.Downloader.LocalRoot}}
			w.runtime = newTestWatchRuntime(cfg, w.opts, storage, nil)
			lease, err := w.runtime.proxy.Scheduler().Acquire(ctx, "hold-transfer", 2)
			require.NoError(t, err)
			t.Cleanup(lease.Release)
			localHost, executor, err := application.LocalDownloadHost(ctx, types.DefaultAccount, w.runtime.worker)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, localHost.Stop(context.Background())) })
			w.runtime.local = executor

			host, control, err := application.DownloadControlHost(ctx, types.DefaultAccount, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
			w.opts.DownloadPipeline = control.(ports.DownloadPipeline)
			wrongPeer, albumFailure, emptyHistory := false, false, false
			api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				history, ok := in.(*tg.MessagesGetHistoryRequest)
				if !ok {
					return fmt.Errorf("unexpected network request %T", in)
				}
				require.Equal(t, int64(12), history.Peer.(*tg.InputPeerChannel).ChannelID)
				if history.Limit > 1 && albumFailure {
					return errors.New("album history failed")
				}
				response := &tg.MessagesMessages{}
				if !emptyHistory && history.OffsetID > 55 {
					ids := []int{56}
					if history.Limit > 1 {
						ids = []int{56, 55}
					}
					for _, id := range ids {
						peerID := int64(12)
						if wrongPeer {
							peerID++
						}
						message := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: peerID}, Message: "album"}
						message.SetGroupedID(77)
						media := &tg.MessageMediaDocument{}
						media.SetDocument(&tg.Document{ID: int64(id), AccessHash: 99, FileReference: []byte("ref"), MimeType: "video/mp4", Size: 4, DCID: 2, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: testVideoFile}}})
						message.SetMedia(media)
						response.Messages = append(response.Messages, message)
					}
				}
				buffer := &bin.Buffer{}
				if err := response.Encode(buffer); err != nil {
					return err
				}
				return out.Decode(buffer)
			}))
			w.pool = liveSinglePool{api: api}
			w.manager = peers.Options{Cache: &peers.InmemoryCache{}}.Build(api)
			require.NoError(t, w.manager.Apply(ctx, nil, []tg.ChatClass{&tg.Channel{ID: 12, AccessHash: 34, Title: "source"}}))
			// Use the same synchronous result consumer as the production connection.
			intents, port, _, err := application.IntentHost(ctx, types.DefaultAccount, func(context.Context, types.DownloadIntent) error { return nil }, nil, w.processDownloadIntent)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, intents.Stop(context.Background())) })
			request := types.DownloadIntent{Account: types.DefaultAccount, Peer: types.MessagePeer{Kind: peerKindChannel, ID: 12, AccessHash: 34}, PeerID: 12, MessageID: 56}
			if skipSame {
				require.NoError(t, os.WriteFile(filepath.Join(cfg.Downloader.LocalRoot, testVideoFile), []byte("same"), 0o600))
			}
			result, err := port.(ports.DownloadRequests).Submit(ctx, request)
			require.NoError(t, err)
			require.Equal(t, 2, result.Total)
			require.Equal(t, map[bool]int{true: 1, false: 2}[skipSame], result.Queued)
			require.Equal(t, map[bool]int{true: 1, false: 0}[skipSame], result.Skipped)
			records, err := taskhub.NewLocalRepository(storage).Records(ctx)
			require.NoError(t, err)
			require.Len(t, records, result.Queued)
			names := map[string]bool{}
			for _, record := range records {
				names[record.Out] = true
				require.Equal(t, types.LocalDownloadStatusQueued, record.Status)
				require.Equal(t, filepath.Join(cfg.Downloader.LocalRoot, record.Out), record.Path)
			}
			require.True(t, names["video (2).mp4"], "collision handling must precede skip-same checks")
			require.NoDirExists(t, cfg.Aria2.Dir)
			wrongPeer = true
			_, err = port.(ports.DownloadRequests).Submit(ctx, request)
			require.ErrorContains(t, err, "source")
			wrongPeer, albumFailure = false, true
			_, err = port.(ports.DownloadRequests).Submit(ctx, request)
			require.ErrorContains(t, err, "album history failed")
			albumFailure, emptyHistory = false, true
			_, err = port.(ports.DownloadRequests).Submit(ctx, request)
			require.ErrorIs(t, err, tutil.ErrMessageDeleted)
		})
	}
}
