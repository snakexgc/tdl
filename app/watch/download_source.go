package watch

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
)

type watchDownloadSource struct{ watcher *Watcher }

func (s watchDownloadSource) Collect(ctx context.Context, request types.DownloadIntent) ([]ports.DownloadMedia, ports.DownloadRegistration, error) {
	w := s.watcher
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if request.Account != w.reactionAccount() || !w.downloadEnabled() {
		return nil, nil, fmt.Errorf("download source account is unavailable")
	}
	peer := protocolPeer(request.Peer)
	if peer == nil {
		var err error
		peer, err = w.resolvePeer(ctx, request.PeerID)
		if err != nil {
			return nil, nil, err
		}
	}
	expected := plainPeer(peer)
	if request.PeerID != 0 && request.PeerID != expected.ID {
		return nil, nil, fmt.Errorf("download peer identity mismatch")
	}
	msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), peer, request.MessageID)
	if err != nil {
		return nil, nil, err
	}
	if msg == nil || msg.ID != request.MessageID {
		return nil, nil, fmt.Errorf("download message identity mismatch")
	}
	if actual := forwardSourcePeer(msg.PeerID); actual.Kind != expected.Kind || actual.ID != expected.ID {
		return nil, nil, fmt.Errorf("download message does not match its source")
	}
	messages := []*tg.Message{msg}
	if _, grouped := msg.GetGroupedID(); grouped {
		messages, err = tutil.GetGroupedMessages(ctx, w.pool.Default(ctx), peer, msg)
		if err != nil {
			return nil, nil, err
		}
	}
	registration := &watchDownloadRegistration{watcher: w, files: make(map[string]fileTask, len(messages))}
	var items []ports.DownloadMedia
	for _, message := range messages {
		if message == nil {
			return nil, nil, fmt.Errorf("empty download album message")
		}
		actual := forwardSourcePeer(message.PeerID)
		if actual.Kind != expected.Kind || actual.ID != expected.ID {
			return nil, nil, fmt.Errorf("download message does not match its source")
		}
		media, ok := tmedia.GetMedia(message)
		if !ok {
			continue
		}
		file := fileTask{msg: message, triggerMsg: msg, media: media, peer: peer, peerID: expected.ID}
		data := w.downloadDirData(ctx, file)
		token := strconv.Itoa(message.ID)
		if _, exists := registration.files[token]; exists {
			continue
		}
		registration.files[token] = file
		items = append(items, ports.DownloadMedia{Token: token, Data: namingMetadata(data.ID, expected.ID, data.Name, data.Time, message, msg, media)})
	}
	return items, registration, nil
}

// Each immutable collection keeps SDK media alive only for its batch. The
// durable proxy repository owns media after Register succeeds.
type watchDownloadRegistration struct {
	watcher *Watcher
	files   map[string]fileTask
}

func (s *watchDownloadRegistration) Register(ctx context.Context, token, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !s.watcher.downloadEnabled() {
		return "", fmt.Errorf("download source is disabled")
	}
	file, ok := s.files[token]
	if !ok {
		return "", fmt.Errorf("download source token is unavailable")
	}
	task, err := s.watcher.runtime.proxy.NewTask(ctx, file.peerID, file.msg.ID, file.peer, name, file.media.Size, file.media)
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

func (s *watchDownloadRegistration) URL(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.watcher.runtime.proxy.BuildURL(id)
}
