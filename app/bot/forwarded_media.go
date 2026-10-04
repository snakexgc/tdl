package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/snakexgc/tdl/interfaces/types"
)

func handleForwardedMediaSubmission(ctx *th.Context, msg *telego.Message, watchCtrl watchControl, account types.AccountID) (bool, error) {
	media, ok := forwardedMedia(msg)
	if !ok {
		return false, nil
	}
	if watchCtrl == nil {
		return true, sendMessage(ctx, msg.Chat.ID, "无法提交转发文件：监听下载控制器未配置。")
	}
	if media.FileSize <= 0 {
		file, err := ctx.Bot().GetFile(ctx, &telego.GetFileParams{FileID: media.FileID})
		if err != nil || file == nil || file.FileSize <= 0 {
			return true, sendMessage(ctx, msg.Chat.ID, "无法读取转发文件大小，未提交下载任务。可尝试发送原消息链接。")
		}
		media.FileSize = file.FileSize
		if file.FileID != "" {
			media.FileID = file.FileID
		}
	}
	request := types.DownloadIntent{
		Account: account, PeerID: msg.Chat.ID, MessageID: msg.MessageID,
		Link: fmt.Sprintf("转发消息 %d", msg.MessageID), Source: types.DownloadSourceBotForward,
		Forwarded: media,
	}
	submitCtx, cancel := context.WithTimeout(ctx, messageLinkSubmissionTimeout)
	result, err := watchCtrl.SubmitForwardedMedia(submitCtx, request)
	cancel()
	if err != nil {
		if result.Total > 0 {
			return true, sendMessage(ctx, msg.Chat.ID, formatMessageLinkSubmissionResult([]types.DownloadSubmissionSummary{result}, []string{fmt.Sprintf("提交未全部完成：%v", err)}))
		}
		return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("转发文件提交失败：%v。可尝试发送原消息链接。", err))
	}
	return true, sendMessage(ctx, msg.Chat.ID, formatMessageLinkSubmissionResult([]types.DownloadSubmissionSummary{result}, nil))
}

func forwardedMedia(msg *telego.Message) (*types.ForwardedMedia, bool) {
	if msg == nil || msg.ForwardOrigin == nil {
		return nil, false
	}
	media := &types.ForwardedMedia{
		Caption: msg.Caption, Date: msg.ForwardOrigin.OriginalDate(),
		AlbumID: msg.MediaGroupID, PeerName: forwardedPeerName(msg.ForwardOrigin),
	}
	switch {
	case msg.Animation != nil:
		media.FileID, media.FileName, media.FileSize, media.MIMEType = msg.Animation.FileID, msg.Animation.FileName, msg.Animation.FileSize, msg.Animation.MimeType
	case msg.Audio != nil:
		media.FileID, media.FileName, media.FileSize, media.MIMEType = msg.Audio.FileID, msg.Audio.FileName, msg.Audio.FileSize, msg.Audio.MimeType
	case msg.Document != nil:
		media.FileID, media.FileName, media.FileSize, media.MIMEType = msg.Document.FileID, msg.Document.FileName, msg.Document.FileSize, msg.Document.MimeType
	case msg.Video != nil:
		media.FileID, media.FileName, media.FileSize, media.MIMEType = msg.Video.FileID, msg.Video.FileName, msg.Video.FileSize, msg.Video.MimeType
	case msg.VideoNote != nil:
		media.FileID, media.FileSize, media.MIMEType = msg.VideoNote.FileID, int64(msg.VideoNote.FileSize), "video/mp4"
	case msg.Voice != nil:
		media.FileID, media.FileSize, media.MIMEType = msg.Voice.FileID, msg.Voice.FileSize, msg.Voice.MimeType
	case len(msg.Photo) > 0:
		photo := msg.Photo[len(msg.Photo)-1]
		media.FileID, media.FileSize, media.MIMEType = photo.FileID, int64(photo.FileSize), "image/jpeg"
	case msg.Sticker != nil:
		media.FileID, media.FileSize = msg.Sticker.FileID, int64(msg.Sticker.FileSize)
		switch {
		case msg.Sticker.IsAnimated:
			media.MIMEType = "application/x-tgsticker"
		case msg.Sticker.IsVideo:
			media.MIMEType = "video/webm"
		default:
			media.MIMEType = "image/webp"
		}
	default:
		return nil, false
	}
	return media, media.FileID != ""
}

func forwardedPeerName(origin telego.MessageOrigin) string {
	switch value := origin.(type) {
	case *telego.MessageOriginChannel:
		return value.Chat.Title
	case *telego.MessageOriginChat:
		return value.SenderChat.Title
	case *telego.MessageOriginUser:
		if value.SenderUser.Username != "" {
			return value.SenderUser.Username
		}
		return strings.TrimSpace(value.SenderUser.FirstName + " " + value.SenderUser.LastName)
	case *telego.MessageOriginHiddenUser:
		return value.SenderUserName
	default:
		return ""
	}
}
