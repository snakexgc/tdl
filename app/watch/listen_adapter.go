package watch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
)

type listenTransport struct{ watcher *Watcher }

func listenError(err error) error {
	if err == nil {
		return nil
	}
	if delay, ok := tgerr.AsFloodWait(err); ok {
		return &ports.ListenFloodWait{Wait: delay, Err: err}
	}
	if tgerr.Is(err, "CHANNEL_PRIVATE", "USER_BANNED_IN_CHANNEL", "CHAT_ID_INVALID", "PEER_ID_INVALID") {
		return errors.Join(ports.ErrListenInaccessible, err)
	}
	if tgerr.Is(err, "MSG_ID_INVALID", "MESSAGE_ID_INVALID", "TOPIC_ID_INVALID") {
		return errors.Join(ports.ErrListenNoDiscussion, err)
	}
	return err
}

func (t listenTransport) resolve(ctx context.Context, ref types.ChatRef) (peers.Peer, error) {
	parts := strings.Split(string(ref), ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("无效监听来源")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("无效监听来源 ID")
	}
	switch parts[0] {
	case peerKindUser:
		return t.watcher.manager.ResolveUserID(ctx, id)
	case peerKindChat:
		return t.watcher.manager.ResolveChatID(ctx, id)
	case peerKindChannel:
		return t.watcher.manager.ResolveChannelID(ctx, id)
	default:
		return nil, fmt.Errorf("无效监听来源类型")
	}
}

func (t listenTransport) input(ctx context.Context, peer types.MessagePeer) (tg.InputPeerClass, error) {
	if peer.Kind == peerKindChat || peer.AccessHash != 0 {
		return protocolPeer(peer), nil
	}
	resolved, err := t.resolve(ctx, types.ChatRef(fmt.Sprintf("%s:%d", peer.Kind, peer.ID)))
	if err != nil {
		return nil, listenError(err)
	}
	return resolved.InputPeer(), nil
}

func (t listenTransport) ResolveSource(ctx context.Context, ref types.ChatRef, comments bool) (types.ListenPeer, error) {
	peer, err := t.resolve(ctx, ref)
	if err != nil {
		return types.ListenPeer{}, listenError(err)
	}
	result := types.ListenPeer{Peer: plainPeer(peer.InputPeer()), Title: peer.VisibleName()}
	if channel, ok := peer.(peers.Channel); ok {
		result.Broadcast = channel.IsBroadcast()
		if comments && result.Broadcast {
			full, err := channel.FullRaw(ctx)
			if err != nil {
				return result, listenError(err)
			}
			if linked, ok := full.GetLinkedChatID(); ok && linked != 0 {
				result.Discussion = types.MessagePeer{Kind: peerKindChannel, ID: linked}
			}
		}
	}
	return result, nil
}

func (w *Watcher) listenMessage(message *tg.Message, entities tg.Entities) types.ListenMessage {
	peer := plainPeer(w.peerToInputPeer(message.PeerID, entities))
	if peer.ID == 0 {
		peer = forwardSourcePeer(message.PeerID)
	}
	result := types.ListenMessage{Peer: peer, ID: message.ID, Date: int64(message.Date), Outgoing: message.Out, Broadcast: message.Post, Pending: message.VideoProcessingPending, Video: tmedia.IsVideo(message)}
	if channel, ok := entities.Channels[peer.ID]; ok {
		result.Broadcast = channel.Broadcast
	}
	if media, ok := tmedia.GetMedia(message); ok {
		result.MediaID = tmedia.Identity(media)
		result.FileName, result.FileSize = media.Name, media.Size
	}
	if reply, ok := message.ReplyTo.(*tg.MessageReplyHeader); ok {
		result.ReplyTo, result.ThreadID = reply.ReplyToMsgID, reply.ReplyToTopID
	}
	if forward, ok := message.GetFwdFrom(); ok {
		result.ForwardPeer, result.ForwardID, result.ForwardDate = forwardSourcePeer(forward.FromID), forward.ChannelPost, int64(forward.Date)
	}
	return result
}

func (t listenTransport) page(ctx context.Context, result tg.MessagesMessagesClass) (types.ListenPage, error) {
	var messages []tg.MessageClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch data := result.(type) {
	case *tg.MessagesMessages:
		messages, users, chats = data.Messages, data.Users, data.Chats
	case *tg.MessagesMessagesSlice:
		messages, users, chats = data.Messages, data.Users, data.Chats
	case *tg.MessagesChannelMessages:
		messages, users, chats = data.Messages, data.Users, data.Chats
	case *tg.MessagesMessagesNotModified:
		return types.ListenPage{Done: true}, nil
	default:
		return types.ListenPage{}, fmt.Errorf("不支持的监听消息响应")
	}
	if err := t.watcher.manager.Apply(ctx, users, chats); err != nil {
		return types.ListenPage{}, err
	}
	entities := tg.Entities{Users: map[int64]*tg.User{}, Chats: map[int64]*tg.Chat{}, Channels: map[int64]*tg.Channel{}}
	for _, user := range users {
		if user, ok := user.(*tg.User); ok {
			entities.Users[user.ID] = user
		}
	}
	for _, chat := range chats {
		switch chat := chat.(type) {
		case *tg.Channel:
			entities.Channels[chat.ID] = chat
		case *tg.Chat:
			entities.Chats[chat.ID] = chat
		}
	}
	page := types.ListenPage{Done: len(messages) < 100}
	for _, raw := range messages {
		id := raw.GetID()
		if id > 0 && (page.NextOffset == 0 || id < page.NextOffset) {
			page.NextOffset = id
		}
		if message, ok := raw.(*tg.Message); ok {
			page.Messages = append(page.Messages, t.watcher.listenMessage(message, entities))
		}
	}
	return page, nil
}

func (t listenTransport) History(ctx context.Context, peer types.MessagePeer, offset, minID int) (types.ListenPage, error) {
	input, err := t.input(ctx, peer)
	if err != nil {
		return types.ListenPage{}, err
	}
	result, err := t.watcher.pool.Default(ctx).MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: input, OffsetID: offset, MinID: minID, Limit: 100})
	if err != nil {
		return types.ListenPage{}, listenError(err)
	}
	return t.page(ctx, result)
}

func (t listenTransport) Discussion(ctx context.Context, peer types.MessagePeer, id int) (types.ListenThreadRecord, error) {
	input, err := t.input(ctx, peer)
	if err != nil {
		return types.ListenThreadRecord{}, err
	}
	result, err := t.watcher.pool.Default(ctx).MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{Peer: input, MsgID: id})
	if err != nil {
		return types.ListenThreadRecord{}, listenError(err)
	}
	if err := t.watcher.manager.Apply(ctx, result.Users, result.Chats); err != nil {
		return types.ListenThreadRecord{}, err
	}
	for index := len(result.Messages) - 1; index >= 0; index-- {
		message, ok := result.Messages[index].(*tg.Message)
		if !ok {
			continue
		}
		group, ok := message.PeerID.(*tg.PeerChannel)
		if !ok || group.ChannelID == peer.ID {
			continue
		}
		discussion, err := t.watcher.manager.ResolveChannelID(ctx, group.ChannelID)
		if err != nil {
			return types.ListenThreadRecord{}, listenError(err)
		}
		return types.ListenThreadRecord{Peer: plainPeer(discussion.InputPeer()), RootID: message.ID}, nil
	}
	return types.ListenThreadRecord{}, ports.ErrListenNoDiscussion
}

func (t listenTransport) Replies(ctx context.Context, peer types.MessagePeer, root, offset, minID int) (types.ListenPage, error) {
	input, err := t.input(ctx, peer)
	if err != nil {
		return types.ListenPage{}, err
	}
	result, err := t.watcher.pool.Default(ctx).MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: input, MsgID: root, OffsetID: offset, MinID: minID, Limit: 100})
	if err != nil {
		return types.ListenPage{}, listenError(err)
	}
	return t.page(ctx, result)
}

func (t listenTransport) Message(ctx context.Context, peer types.MessagePeer, id int) (types.ListenMessage, error) {
	input, err := t.input(ctx, peer)
	if err != nil {
		return types.ListenMessage{}, err
	}
	var result tg.MessagesMessagesClass
	if channel, ok := input.(*tg.InputPeerChannel); ok {
		result, err = t.watcher.pool.Default(ctx).ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: id}}})
	} else {
		result, err = t.watcher.pool.Default(ctx).MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: id}})
	}
	if err != nil {
		return types.ListenMessage{}, listenError(err)
	}
	page, err := t.page(ctx, result)
	if err != nil {
		return types.ListenMessage{}, err
	}
	for _, message := range page.Messages {
		if message.ID == id && message.Peer.ID == peer.ID && message.Peer.Kind == peer.Kind {
			return message, nil
		}
	}
	return types.ListenMessage{}, ports.ErrListenDeleted
}

type listenDownloads struct{ watcher *Watcher }

func (d listenDownloads) Submit(ctx context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	d.watcher.intentMu.RLock()
	intents := d.watcher.intents
	d.watcher.intentMu.RUnlock()
	requests, ok := intents.(ports.DownloadRequests)
	if !ok {
		return types.DownloadSubmissionSummary{}, fmt.Errorf("下载触发服务不可用")
	}
	return requests.Submit(ctx, request)
}

func (w *Watcher) listenAccepted(ctx context.Context, id string) (bool, error) {
	if _, exists, err := taskhub.NewLocalRepository(w.runtime.store).Get(ctx, id); err != nil || exists {
		return exists, err
	}
	records, err := taskhub.NewAria2Repository(w.runtime.store, 0).Records(ctx)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.TaskID == id {
			return true, nil
		}
	}
	return false, nil
}
