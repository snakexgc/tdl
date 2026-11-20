package httpdl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestBotFileStreamsWithMatchingAuthorizationAndRefresh(t *testing.T) {
	ctx := context.Background()
	proxy := newDownloadProxy(config.HTTPConfig{}, 1, 1, nil, nil, nil)
	media := &tmedia.Media{InputFileLoc: &tg.InputDocumentFileLocation{ID: 78, AccessHash: 90, FileReference: []byte("http-bot")}, Name: "file.bin", Size: 4, DC: 2}
	task, err := proxy.NewTaskWithPriority(ctx, 42, 17, nil, media.Name, media.Size, media, 0, &types.ForwardedMedia{BotID: 100, FileID: "file-id"})
	require.NoError(t, err)
	persisted, err := persistentDownloadTaskFromTask(task)
	require.NoError(t, err)
	restored, err := persisted.ToTask()
	require.NoError(t, err)
	require.Equal(t, task.BotFileID, restored.BotFileID)
	require.Equal(t, task.BotID, restored.BotID)
	require.Nil(t, restored.Peer)

	messages := 0
	var documentID int64 = 78
	var forwardedChat int64 = 42
	invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.MessagesGetMessagesRequest:
			messages++
			ref := fmt.Sprintf("bot-session-%d", messages)
			message := &tg.Message{ID: 17, PeerID: &tg.PeerUser{UserID: forwardedChat}, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: documentID, DCID: 2, Size: 4, AccessHash: 123, FileReference: []byte(ref)}}}
			message.SetMedia(message.Media)
			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{message}}
		case *tg.UploadGetFileRequest:
			loc := req.Location.(*tg.InputDocumentFileLocation)
			if loc.AccessHash != 123 {
				return fmt.Errorf("foreign file reference")
			}
			if string(loc.FileReference) == "bot-session-1" {
				return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			output.(*tg.UploadFileBox).File = &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte("data")}
		default:
			return fmt.Errorf("unexpected request %T", req)
		}
		return nil
	})
	session := NewBotMediaConnection(testDownloadPool{client: tg.NewClient(invoker)})
	botPool := func(context.Context) (*BotMediaConnection, error) { return session, nil }
	proxy.SetBotMedia(task.BotID, botPool)
	// The bot alone supplies both readiness and file bytes for the HTTP endpoint.
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/download/"+task.ID, nil)
	req.Header.Set("Range", "bytes=1-3")
	proxy.routes().ServeHTTP(recorder, req)
	require.Equal(t, 206, recorder.Code)
	require.Equal(t, "ata", recorder.Body.String())
	require.Equal(t, 2, messages)
	lease, err := proxy.scheduler.Acquire(ctx, task.ID, media.DC)
	require.NoError(t, err)
	defer lease.Release()
	var output bytes.Buffer
	require.NoError(t, proxy.StreamParallel(ctx, restored, lease, 0, 3, &output))
	require.Equal(t, "data", output.String())
	require.Equal(t, 2, messages, "parallel range should share the refreshed bot reference")
	session = NewBotMediaConnection(testDownloadPool{client: tg.NewClient(invoker)})
	proxy.SetBotMedia(200, botPool)
	require.ErrorContains(t, proxy.Stream(ctx, restored, lease, 0, 3, &output), "another bot")
	require.Equal(t, 2, messages)
	// A legacy record lacks a bot ID; verify the exact chat, message and media
	// through the current bot before any bytes are fetched.
	restored.BotID = 0
	forwardedChat = 45
	require.ErrorContains(t, proxy.Stream(ctx, restored, lease, 0, 3, &output), "unavailable")
	forwardedChat = 42
	documentID = 79
	require.ErrorContains(t, proxy.Stream(ctx, restored, lease, 0, 3, &output), "media changed")
}

func TestForwardedPhotoLooksUpTheSelectedSizeInItsChannel(t *testing.T) {
	msg := &tg.Message{ID: 12, PeerID: &tg.PeerChannel{ChannelID: 17}}
	msg.SetMedia(&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 78, DCID: 2, AccessHash: 123, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "m", Size: 64}, &tg.PhotoSizeProgressive{Type: "y", Sizes: []int{128, 256}},
	}}})
	client := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req := input.(*tg.ChannelsGetMessagesRequest)
		require.EqualValues(t, 17, req.Channel.(*tg.InputChannel).ChannelID)
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{msg}}
		return nil
	}))
	task := &downloadTask{ID: "photo_78_m", PeerID: -1000000000017, MessageID: 12, FileSize: 64, Media: &tmedia.Media{DC: 2, InputFileLoc: &tg.InputPhotoFileLocation{ID: 78, ThumbSize: "m"}}}
	media, err := botMessageMedia(context.Background(), client, task)
	require.NoError(t, err)
	require.EqualValues(t, 123, media.InputFileLoc.(*tg.InputPhotoFileLocation).AccessHash)
}
