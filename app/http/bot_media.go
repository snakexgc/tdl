package httpdl

import (
	"context"
	"errors"
	"io"
	"sync/atomic"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/internal/core/dcpool"
	"github.com/snakexgc/tdl/internal/core/tmedia"
)

// BotMediaConnection keeps MTProto file references within the authorization
// that resolved them. Reconnecting creates a new pool and reference cache.
type BotMediaConnection struct {
	pool    dcpool.Pool
	sources *sourceRegistry
}

func NewBotMediaConnection(pool dcpool.Pool) *BotMediaConnection {
	return &BotMediaConnection{pool: pool, sources: newSourceRegistry()}
}

type botMediaBinding struct {
	id      int64
	connect func(context.Context) (*BotMediaConnection, error)
	active  atomic.Pointer[BotMediaConnection]
}

func (p *downloadProxy) SetBotMedia(id int64, connect func(context.Context) (*BotMediaConnection, error)) {
	p.botFileMu.Lock()
	defer p.botFileMu.Unlock()
	p.botMedia = nil
	if connect != nil && id > 0 {
		p.botMedia = &botMediaBinding{id: id, connect: connect}
	}
}

func (t *downloadTask) isBotFile() bool { return t.Peer == nil && t.BotFileID != "" }

func (p *downloadProxy) botMediaFor(task *downloadTask) (*botMediaBinding, error) {
	p.botFileMu.RLock()
	defer p.botFileMu.RUnlock()
	b := p.botMedia
	if b == nil {
		return nil, errors.New("bot media connection unavailable")
	}
	if task.BotID != 0 && task.BotID != b.id {
		return nil, errors.New("forwarded file belongs to another bot")
	}
	return b, nil
}

func (p *downloadProxy) streamBotFile(ctx context.Context, task *downloadTask, lease *comif.TaskLease, start, end int64, w io.Writer, parallel bool) error {
	b, err := p.botMediaFor(task)
	if err != nil {
		return err
	}
	connection, err := b.connect(ctx)
	if err != nil {
		return err
	}
	b.active.Store(connection)
	pool := connection.pool
	refresh := func(ctx context.Context) (*tmedia.Media, error) {
		return botMessageMedia(ctx, pool.Default(ctx), task)
	}
	// The Bot API and this MTProto client have separate authorizations. Fetch
	// the message through the actual streaming session before using a location.
	source := *task
	source.Media = nil
	handle := connection.sources.Acquire(&source, refresh)
	defer handle.Release()
	if err := handle.Source().refreshMedia(ctx, nil); err != nil {
		return err
	}
	if parallel {
		return streamTelegramMediaParallel(ctx, pool, handle.Source(), lease, p.telegramFileErrorReporter(), start, end, w)
	}
	return streamTelegramMedia(ctx, pool, handle.Source(), lease, p.telegramFileErrorReporter(), start, end, w)
}

func botMessageMedia(ctx context.Context, client *tg.Client, task *downloadTask) (*tmedia.Media, error) {
	peer := constant.TDLibPeerID(task.PeerID)
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: task.MessageID}}
	var result tg.MessagesMessagesClass
	var err error
	switch {
	case peer.IsChannel():
		// Telegram permits bots to use a zero access hash for known peers.
		result, err = client.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ToPlain()}, ID: ids})
	case peer.IsUser(), peer.IsChat():
		result, err = client.MessagesGetMessages(ctx, ids)
	default:
		return nil, errors.New("invalid forwarded message chat")
	}
	if err != nil {
		return nil, err
	}
	if page, ok := result.AsModified(); ok {
		for _, raw := range page.GetMessages() {
			msg, ok := raw.(*tg.Message)
			if !ok || msg.ID != task.MessageID || botChatID(msg.PeerID) != peer {
				continue
			}
			media, ok := forwardedMessageVariant(msg, task.Media)
			if !ok || tmedia.Identity(media) != task.ID || media.DC != task.Media.DC || media.Size != task.FileSize {
				return nil, errors.New("forwarded message media changed")
			}
			return media, nil
		}
	}
	return nil, errors.New("forwarded message is unavailable")
}

func forwardedMessageVariant(msg *tg.Message, expected *tmedia.Media) (*tmedia.Media, bool) {
	if photo, ok := msg.Media.(*tg.MessageMediaPhoto); ok {
		location, ok := expected.InputFileLoc.(*tg.InputPhotoFileLocation)
		if !ok {
			return nil, false
		}
		return tmedia.PhotoVariant(photo, location.ThumbSize)
	}
	return tmedia.GetMedia(msg)
}

func botChatID(peer tg.PeerClass) (id constant.TDLibPeerID) {
	switch p := peer.(type) {
	case *tg.PeerUser:
		id.User(p.UserID)
	case *tg.PeerChat:
		id.Chat(p.ChatID)
	case *tg.PeerChannel:
		id.Channel(p.ChannelID)
	}
	return id
}
