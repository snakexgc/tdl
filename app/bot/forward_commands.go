package bot

import (
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	appforward "github.com/iyear/tdl/app/forward"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

func handleForwardCommand(ctx *th.Context, msg *telego.Message, text string, namespaceKV storage.Storage) (bool, error) {
	cmd, _, payload := tu.ParseCommandPayload(text)
	if "/"+cmd != botCmdForward {
		return false, nil
	}

	target, err := forwardTargetFromPayload(payload)
	if err != nil {
		return true, sendMessage(ctx, msg.Chat.ID, forwardUsage())
	}
	if target == "" {
		target = config.Get().Forward.Target
	}

	sourceText := forwardSourceText(msg)
	links := extractHTTPLinks(sourceText)
	normalized := make([]string, 0, len(links))
	for _, link := range links {
		link, err := watch.ValidateTelegramMessageHTTPLink(link)
		if err == nil {
			normalized = append(normalized, link)
		}
	}
	if len(normalized) == 0 {
		return true, sendMessage(ctx, msg.Chat.ID, forwardUsage())
	}
	if namespaceKV == nil {
		return true, sendMessage(ctx, msg.Chat.ID, "转发失败：Telegram 用户数据未准备好。")
	}

	cfg := config.Get()
	ids, err := appforward.Jobs().EnqueueLinks(ctx, normalized, target, "", config.EffectiveForwardMode(cfg), cfg.Forward.Silent)
	if err != nil {
		return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("加入转发队列失败：%v", err))
	}

	return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("已加入转发队列：%d 条，将按顺序逐个转发。可在 Web 管理面板的「转发监控」查看进度与暂停/继续/删除。", len(ids)))
}

func forwardTargetFromPayload(payload string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(payload))
	switch len(fields) {
	case 0:
		return "", nil
	case 1:
		return fields[0], nil
	default:
		return "", errors.New("too many forward command arguments")
	}
}

func forwardSourceText(msg *telego.Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	if msg.ReplyToMessage != nil {
		parts = append(parts, msg.ReplyToMessage.Text, msg.ReplyToMessage.Caption)
	}
	return strings.Join(parts, "\n")
}

func forwardUsage() string {
	return "用法：回复一条包含 Telegram 消息链接的消息，发送 /forward [目标]。\n目标不填时使用 forward.target；forward.target 为空时转发到收藏夹。"
}
