package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/iyear/tdl/app/watch"
)

const messageLinkSubmissionTimeout = 2 * time.Minute

func handleMessageLinkSubmission(ctx *th.Context, msg *telego.Message, text string, watchCtrl watchControl) (bool, error) {
	if msg.Document != nil && isTorrentDocument(msg.Document) {
		return true, sendMessage(ctx, msg.Chat.ID, "已不再支持直接提交 .torrent 文件。请发送包含目标文件的 Telegram 消息链接。")
	}

	links := extractHTTPLinks(text)
	if len(links) == 0 {
		if containsMagnetLink(text) {
			return true, sendMessage(ctx, msg.Chat.ID, "已不再支持直接提交磁力链接。请发送包含目标文件的 Telegram 消息链接。")
		}
		return false, nil
	}

	var accepted []watch.MessageLinkSubmissionResult
	var rejected []string
	for _, link := range links {
		normalized, err := watch.ValidateTelegramMessageHTTPLink(link)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s：不是 Telegram 消息链接，原因：%v", link, err))
			continue
		}
		if watchCtrl == nil {
			rejected = append(rejected, fmt.Sprintf("%s：无法提交，监听下载控制器未配置", normalized))
			continue
		}

		submitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), messageLinkSubmissionTimeout)
		result, err := watchCtrl.SubmitMessageLink(submitCtx, normalized)
		cancel()
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s：提交失败：%v", normalized, err))
			continue
		}
		accepted = append(accepted, result)
	}

	return true, sendMessage(ctx, msg.Chat.ID, formatMessageLinkSubmissionResult(accepted, rejected))
}

func extractHTTPLinks(text string) []string {
	fields := strings.Fields(text)
	seen := map[string]struct{}{}
	var links []string
	for _, field := range fields {
		field = strings.Trim(field, "<>()[]{}\"'.,;，。；")
		lower := strings.ToLower(field)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		links = append(links, field)
	}
	return links
}

func containsMagnetLink(text string) bool {
	for _, field := range strings.Fields(text) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(field)), "magnet:?xt=urn:btih:") {
			return true
		}
	}
	return false
}

func formatMessageLinkSubmissionResult(accepted []watch.MessageLinkSubmissionResult, rejected []string) string {
	parts := make([]string, 0, len(accepted)+len(rejected)+2)
	for _, result := range accepted {
		if result.Total == 0 {
			parts = append(parts, fmt.Sprintf("%s：是 Telegram 消息链接，但消息中没有可下载媒体。", result.Link))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s：已按 watch 流程提交。文件总数：%d，需要下载：%d，跳过：%d。", result.Link, result.Total, result.Queued, result.Skipped))
	}
	parts = append(parts, rejected...)
	if len(parts) == 0 {
		return "没有可处理的 HTTP/HTTPS 链接。"
	}
	return strings.Join(parts, "\n")
}
