package watch

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/fileid"
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
	if request.Forwarded != nil {
		return s.collectForwarded(ctx, request)
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
	automatic := request.Source == types.DownloadSourceListen || request.Source == types.DownloadSourceListenComment
	if _, grouped := msg.GetGroupedID(); grouped && !automatic {
		messages, err = tutil.GetGroupedMessages(ctx, w.pool.Default(ctx), peer, msg)
		if err != nil {
			return nil, nil, err
		}
	}
	registration := &watchDownloadRegistration{watcher: w, priority: types.DownloadPriority(request.Source), files: make(map[string]fileTask, len(messages))}
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
		if request.ExpectedMediaID != "" && tmedia.Identity(media) != request.ExpectedMediaID {
			return nil, nil, fmt.Errorf("监听媒体身份已变化")
		}
		if request.Source == types.DownloadSourceListenComment && !tmedia.IsVideo(message) {
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

func (s watchDownloadSource) collectForwarded(ctx context.Context, request types.DownloadIntent) ([]ports.DownloadMedia, ports.DownloadRegistration, error) {
	forwarded := request.Forwarded
	if request.Source != types.DownloadSourceBotForward || forwarded.FileID == "" || forwarded.FileSize <= 0 {
		return nil, nil, fmt.Errorf("forwarded media metadata is incomplete")
	}
	decoded, err := fileid.DecodeFileID(forwarded.FileID)
	if err != nil {
		return nil, nil, fmt.Errorf("decode forwarded media: %w", err)
	}
	location, ok := decoded.AsInputFileLocation()
	if !ok || decoded.DC <= 0 || decoded.ID == 0 {
		return nil, nil, fmt.Errorf("forwarded media has no supported Telegram file location")
	}
	switch location.(type) {
	case *tg.InputDocumentFileLocation, *tg.InputPhotoFileLocation:
	default:
		return nil, nil, fmt.Errorf("forwarded media location %T is unsupported", location)
	}
	name := strings.TrimSpace(forwarded.FileName)
	if name == "" {
		name = strconv.FormatInt(decoded.ID, 10) + forwardedFileExtension(decoded.Type, forwarded.MIMEType)
	}
	media := &tmedia.Media{InputFileLoc: location, Name: name, Size: forwarded.FileSize, DC: decoded.DC, Date: forwarded.Date}
	message := &tg.Message{ID: request.MessageID, Date: int(forwarded.Date), Message: forwarded.Caption}
	if message.Date == 0 {
		message.Date = int(ports.Now(ctx).Unix())
	}
	peerName := safePathSegment(forwarded.PeerName)
	if peerName == "" {
		peerName = strconv.FormatInt(request.PeerID, 10)
	}
	directoryID := strconv.FormatInt(request.PeerID, 10)
	data := namingMetadata(directoryID, request.PeerID, peerName, ports.Now(ctx), message, message, media)
	data.AlbumID = forwarded.AlbumID
	file := fileTask{msg: message, triggerMsg: message, media: media, peerID: request.PeerID, botFileID: forwarded.FileID}
	registration := &watchDownloadRegistration{watcher: s.watcher, files: map[string]fileTask{"forwarded": file}}
	return []ports.DownloadMedia{{Token: "forwarded", Data: data}}, registration, nil
}

func forwardedFileExtension(kind fileid.Type, mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "application/x-tgsticker":
		return ".tgs"
	}
	switch kind {
	case fileid.Photo:
		return ".jpg"
	case fileid.Video, fileid.VideoNote, fileid.Animation:
		return ".mp4"
	case fileid.Voice:
		return ".ogg"
	case fileid.Audio:
		return ".mp3"
	case fileid.Sticker:
		return ".webp"
	default:
		return ".unknown"
	}
}

// Each immutable collection keeps SDK media alive only for its batch. The
// durable proxy repository owns media after Register succeeds.
type watchDownloadRegistration struct {
	watcher  *Watcher
	priority int
	files    map[string]fileTask
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
	task, err := s.watcher.runtime.proxy.NewTaskWithPriority(ctx, file.peerID, file.msg.ID, file.peer, name, file.media.Size, file.media, s.priority, file.botFileID)
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
