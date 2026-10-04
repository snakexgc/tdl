package watch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	filterrules "github.com/snakexgc/tdl/application/filter.rules"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

type listenRecorder struct {
	err      error
	messages []types.ListenMessage
}

func (l *listenRecorder) Observe(_ context.Context, message types.ListenMessage) error {
	l.messages = append(l.messages, message)
	return l.err
}
func (*listenRecorder) Serve(context.Context, ports.ListenResources) error { return nil }

func TestNewMessageFanoutPreservesForwardingOnListenerFailure(t *testing.T) {
	listener := &listenRecorder{err: errors.New("journal unavailable")}
	forward := &acceptedForwardIntents{}
	w := &Watcher{opts: Options{Account: types.DefaultAccount, Download: true, Forward: true, Listener: listener, ForwardRouting: interestedForwardRouter{}}, forwardIntents: forward}
	message := &tg.Message{ID: 7, Date: 1, PeerID: &tg.PeerChannel{ChannelID: 12}}
	entities := tg.Entities{Channels: map[int64]*tg.Channel{12: {ID: 12, AccessHash: 34}}}
	require.ErrorIs(t, w.onNewChannelMessage(context.Background(), entities, &tg.UpdateNewChannelMessage{Message: message}), listener.err)
	require.Len(t, listener.messages, 1)
	require.Equal(t, 7, forward.received.MessageID)
}

func TestAutomaticSourceIsSingleMessageAndUsesGlobalSizeAndExtensionFilters(t *testing.T) {
	ctx := config.WithSource(context.Background(), config.NewSource(config.DefaultConfig()))
	w := namingWatcher(t, "F", 255)
	w.opts.Download, w.opts.Account = true, types.DefaultAccount
	store := &storage.Memory{}
	w.runtime = newTestWatchRuntime(config.DefaultConfig(), w.opts, store, nil)
	name, size := "clip.mp4", int64(1536*1024)
	grouped := true
	api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		request, ok := in.(*tg.MessagesGetHistoryRequest)
		if !ok {
			return fmt.Errorf("unexpected request %T", in)
		}
		require.Equal(t, 1, request.Limit, "automatic submissions do not expand the album")
		message := &tg.Message{ID: 56, PeerID: &tg.PeerChannel{ChannelID: 12}}
		if grouped {
			message.SetGroupedID(77)
		}
		media := &tg.MessageMediaDocument{}
		media.SetDocument(&tg.Document{
			ID: 56, AccessHash: 99, FileReference: []byte("ref"), MimeType: "application/octet-stream", Size: size, DCID: 2,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}},
		})
		message.SetMedia(media)
		response := &tg.MessagesMessages{Messages: []tg.MessageClass{message}}
		buffer := &bin.Buffer{}
		if err := response.Encode(buffer); err != nil {
			return err
		}
		return out.Decode(buffer)
	}))
	w.pool = liveSinglePool{api: api}
	w.manager = peers.Options{Cache: &peers.InmemoryCache{}}.Build(api)
	require.NoError(t, w.manager.Apply(ctx, nil, []tg.ChatClass{&tg.Channel{ID: 12, AccessHash: 34, Title: "listen source"}}))
	registry := rte.NewRegistry()
	require.NoError(t, filterrules.Register(registry))
	filterHost, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{filterrules.ID: {"include": []string{"mp4"}, "min_mb": 1, "max_mb": 2}})
	require.NoError(t, err)
	require.Equal(t, rte.Running, filterHost.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, filterHost.Stop(context.Background())) })
	filter, err := filterHost.Resolve(ports.FilterRulesName)
	require.NoError(t, err)
	w.opts.Filter = filter.(ports.FilterRules)
	host, control, err := application.DownloadControlHost(ctx, types.DefaultAccount, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	w.opts.DownloadPipeline = control.(ports.DownloadPipeline)
	w.opts.DownloadRouting = &fixedDownloadRoute{ports.DownloadRoute{Executors: []string{config.DownloadExecutorAria2}}}
	accepted := 0
	var priorities []int
	w.opts.DownloadSubmitter = preparedExecutor{name: config.DownloadExecutorAria2, submit: func(_ context.Context, request types.DownloadSubmission) (types.DownloadResult, error) {
		accepted++
		priorities = append(priorities, request.Priority)
		return types.DownloadResult{Target: config.DownloadExecutorAria2, ID: request.TaskID}, nil
	}}
	request := types.DownloadIntent{Account: types.DefaultAccount, Peer: types.MessagePeer{Kind: peerKindChannel, ID: 12, AccessHash: 34}, PeerID: 12, MessageID: 56, Source: types.DownloadSourceListen, ExpectedMediaID: "document_56"}
	result, err := w.processDownloadIntent(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	require.Equal(t, 1, result.Queued)
	for _, excluded := range []struct {
		name string
		size int64
	}{{"clip.png", size}, {"small.mp4", 512 * 1024}, {"large.mp4", 3 * 1024 * 1024}} {
		name, size = excluded.name, excluded.size
		result, err = w.processDownloadIntent(ctx, request)
		require.NoError(t, err)
		require.Equal(t, 1, result.Skipped)
	}
	name, size = "photo.png", 1536*1024
	request.Source = types.DownloadSourceListenComment
	items, _, err := (watchDownloadSource{w}).Collect(ctx, request)
	require.NoError(t, err)
	require.Empty(t, items)
	name = "VIDEO.MP4"
	items, _, err = (watchDownloadSource{w}).Collect(ctx, request)
	require.NoError(t, err)
	require.Len(t, items, 1)
	request.ExpectedMediaID = "document_999"
	_, _, err = (watchDownloadSource{w}).Collect(ctx, request)
	require.ErrorContains(t, err, "身份")
	require.Equal(t, 1, accepted)
	grouped = false
	request.Source, request.ExpectedMediaID = types.DownloadSourceReaction, ""
	_, err = w.processDownloadIntent(ctx, request)
	require.NoError(t, err)
	request.Source = types.DownloadSourceListen
	_, err = w.processDownloadIntent(ctx, request)
	require.NoError(t, err)
	require.Equal(t, []int{0, types.DownloadPriorityReaction, 0}, priorities)
	registered, exists, err := w.runtime.proxy.Tasks().Get(ctx, "document_56")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, types.DownloadPriorityReaction, registered.Priority, "listener replay preserves an explicit request's durable priority")
}
