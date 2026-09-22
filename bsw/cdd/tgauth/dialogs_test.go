package tgauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

type dialogQuery func(context.Context, *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)

func (q dialogQuery) MessagesGetDialogs(ctx context.Context, r *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
	return q(ctx, r)
}

func TestDialogsReadAllPagesAndArchiveWithTypedIDs(t *testing.T) {
	calls, cached := 0, 0
	api := dialogQuery(func(_ context.Context, r *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		calls++
		folder, ok := r.GetFolderID()
		require.True(t, ok)
		require.Equal(t, 100, r.Limit)
		if folder == 1 {
			return &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 1}}}, Chats: []tg.ChatClass{&tg.Channel{ID: 1, Title: "Archive", AccessHash: 9988, Megagroup: true}}}, nil
		}
		if r.OffsetID == 0 {
			require.False(t, r.ExcludePinned)
			return &tg.MessagesDialogsSlice{Count: 3, Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}}}, Messages: []tg.MessageClass{&tg.Message{ID: 11, Date: 50, PeerID: &tg.PeerUser{UserID: 1}}}, Users: []tg.UserClass{&tg.User{ID: 1, FirstName: "Alice", AccessHash: 12345}}}, nil
		}
		require.Equal(t, 11, r.OffsetID)
		require.True(t, r.ExcludePinned)
		return &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChat{ChatID: 1}}, &tg.Dialog{Peer: &tg.PeerUser{UserID: 2}}}, Chats: []tg.ChatClass{&tg.Chat{ID: 1, Title: "Team"}}, Users: []tg.UserClass{&tg.User{ID: 2, FirstName: "Helper", Bot: true, AccessHash: 67890}}}, nil
	})
	items, err := ReadDialogs(context.Background(), api, func(context.Context, []tg.UserClass, []tg.ChatClass) error { cached++; return nil })
	require.NoError(t, err)
	require.Len(t, items, 4)
	require.Equal(t, 3, calls)
	require.Equal(t, 3, cached)
	require.Equal(t, "user:1", string(items[0].Ref))
	require.Equal(t, "group", items[1].Kind)
	require.Equal(t, "bot", items[2].Kind)
	require.Equal(t, "chat:1", string(items[3].Ref))
	data, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(data), "access_hash")
	require.NotContains(t, string(data), "12345")
}

func TestDialogPeerCacheFailureRejectsIncompleteCatalog(t *testing.T) {
	failure := errors.New("cache unavailable")
	api := dialogQuery(func(context.Context, *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
		return &tg.MessagesDialogs{}, nil
	})
	items, err := ReadDialogs(context.Background(), api, func(context.Context, []tg.UserClass, []tg.ChatClass) error { return failure })
	require.ErrorIs(t, err, failure)
	require.Nil(t, items)
}
