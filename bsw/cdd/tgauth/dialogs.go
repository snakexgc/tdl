package tgauth

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type DialogAPI interface {
	MessagesGetDialogs(context.Context, *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)
}

// ReadDialogs follows every page in both the inbox and archive. It caches
// access hashes in the existing account dataset, never exposes them to the UI.
func ReadDialogs(ctx context.Context, api DialogAPI, cache func(context.Context, []tg.UserClass, []tg.ChatClass) error) ([]types.Dialog, error) {
	items := map[types.ChatRef]types.Dialog{}
	for _, folder := range []int{0, 1} {
		complete := false
		query := dialogs.QueryFunc(func(ctx context.Context, req dialogs.Request) (tg.MessagesDialogsClass, error) {
			// The SDK asks once more after consuming a terminal batch.
			if complete {
				return &tg.MessagesDialogs{}, nil
			}
			request := &tg.MessagesGetDialogsRequest{OffsetDate: req.OffsetDate, OffsetID: req.OffsetID, OffsetPeer: req.OffsetPeer, Limit: req.Limit, ExcludePinned: req.OffsetID != 0}
			request.SetFolderID(folder)
			result, err := api.MessagesGetDialogs(ctx, request)
			if err != nil {
				return nil, err
			}
			_, complete = result.(*tg.MessagesDialogs)
			if cache != nil {
				switch data := result.(type) {
				case *tg.MessagesDialogs:
					err = cache(ctx, data.Users, data.Chats)
				case *tg.MessagesDialogsSlice:
					err = cache(ctx, data.Users, data.Chats)
				}
			}
			return result, err
		})
		iterator := dialogs.NewIterator(query, 100)
		for iterator.Next(ctx) {
			elem := iterator.Value()
			var item types.Dialog
			switch peer := elem.Peer.(type) {
			case *tg.InputPeerUser:
				user, ok := elem.Entities.User(peer.UserID)
				if !ok || user.Deleted {
					continue
				}
				item = types.Dialog{Ref: types.ChatRef(fmt.Sprintf("user:%d", user.ID)), Title: strings.TrimSpace(user.FirstName + " " + user.LastName), Username: user.Username, Kind: "user"}
				if user.Bot {
					item.Kind = "bot"
				}
			case *tg.InputPeerChat:
				chat, ok := elem.Entities.Chat(peer.ChatID)
				if !ok || chat.Deactivated {
					continue
				}
				item = types.Dialog{Ref: types.ChatRef(fmt.Sprintf("chat:%d", chat.ID)), Title: chat.Title, Kind: "group"}
			case *tg.InputPeerChannel:
				channel, ok := elem.Entities.Channel(peer.ChannelID)
				if !ok || channel.Left {
					continue
				}
				item = types.Dialog{Ref: types.ChatRef(fmt.Sprintf("channel:%d", channel.ID)), Title: channel.Title, Username: channel.Username, Kind: "channel"}
				if channel.Megagroup {
					item.Kind = "group"
				}
			default:
				continue
			}
			items[item.Ref] = item
		}
		if err := iterator.Err(); err != nil {
			return nil, err
		}
	}
	result := make([]types.Dialog, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Title == result[j].Title {
			return result[i].Ref < result[j].Ref
		}
		return result[i].Title < result[j].Title
	})
	return result, nil
}

func Dialogs(ctx context.Context, client *tg.Client, store storage.Storage) ([]types.Dialog, error) {
	peerStore, err := PeersStore(store)
	if err != nil {
		return nil, err
	}
	manager := peers.Options{Storage: peerStore}.Build(client)
	return ReadDialogs(ctx, client, manager.Apply)
}
