package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	appforward "github.com/iyear/tdl/app/forward"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

const forwardCommandTimeout = 10 * time.Minute

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

	_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(msg.Chat.ID), "正在转发，请稍候..."))
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), forwardCommandTimeout)
	defer cancel()

	cfg := config.Get()
	result, err := appforward.RunLinks(runCtx, appforward.SessionOptions{
		KV:               namespaceKV,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
		PoolSize:         config.EffectivePoolSize(cfg),
		Threads:          config.EffectiveThreads(cfg),
	}, appforward.Request{
		Links:   normalized,
		To:      target,
		Mode:    config.EffectiveForwardMode(cfg),
		Silent:  cfg.Forward.Silent,
		Grouped: true,
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("转发失败：%v", err))
	}

	return true, sendMessage(ctx, msg.Chat.ID, formatForwardResult(result, err))
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

func formatForwardResult(result appforward.Result, runErr error) string {
	parts := []string{
		fmt.Sprintf("转发完成：成功 %d，失败 %d。", result.Submitted, result.Failed),
		fmt.Sprintf("目标：%s", emptyAsSavedMessages(result.Target)),
		fmt.Sprintf("模式：%s", result.Mode),
	}
	if runErr != nil {
		parts = append(parts, fmt.Sprintf("运行结果：%v", runErr))
	}
	for _, item := range result.Items {
		if item.OK {
			parts = append(parts, fmt.Sprintf("%s：已提交", item.Link))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s：失败：%s", item.Link, item.Error))
	}
	return strings.Join(parts, "\n")
}

func emptyAsSavedMessages(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "收藏夹"
	}
	return value
}
