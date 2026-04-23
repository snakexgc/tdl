package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/utils"
)

const aria2CommandTimeout = 30 * time.Second

type aria2ControllerFactory func() *watch.Aria2Controller

func handleAria2Command(ctx *th.Context, msg *telego.Message, text string, factory aria2ControllerFactory) (bool, error) {
	cmd, _, _ := tu.ParseCommandPayload(text)
	switch "/" + cmd {
	case "/aria2", "/aria2_help":
		return true, sendMessage(ctx, msg.Chat.ID, aria2HelpMessage())
	case "/aria2_overview":
		overview, err := runAria2Overview(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取 aria2 任务总览失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2Overview(overview))
	case "/aria2_pause_all":
		result, err := runAria2Action(ctx, factory, func(ctx context.Context, controller *watch.Aria2Controller) (watch.Aria2ActionResult, error) {
			return controller.PauseAll(ctx)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("暂停 TDL aria2 任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2ActionResult("暂停全部", result))
	case "/aria2_start_all":
		result, err := runAria2Action(ctx, factory, func(ctx context.Context, controller *watch.Aria2Controller) (watch.Aria2ActionResult, error) {
			return controller.StartAll(ctx)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("开始 TDL aria2 任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2ActionResult("开始全部", result))
	case "/aria2_retry":
		result, err := runAria2Action(ctx, factory, func(ctx context.Context, controller *watch.Aria2Controller) (watch.Aria2ActionResult, error) {
			return controller.RetryStopped(ctx)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("重试 TDL aria2 任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2ActionResult("重试已停止任务", result))
	default:
		return false, nil
	}
}

func notifyAria2RetryCandidates(ctx context.Context, notifier *botNotifier, factory aria2ControllerFactory) {
	if notifier == nil || factory == nil {
		return
	}

	overview, err := runAria2Overview(ctx, factory)
	if err != nil {
		return
	}
	if len(overview.RetryCandidates) == 0 {
		return
	}

	notifier.Notify(ctx, fmt.Sprintf(
		"检测到 %d 个 TDL 创建的 aria2 任务位于已停止/已完成分组且尚未完成。\n还需下载：%s\n\n发送 /aria2_retry 重试；发送 /aria2_overview 查看总览。",
		len(overview.RetryCandidates),
		utils.Byte.FormatBinaryBytes(overview.RetryBytes),
	))
}

func runAria2Overview(ctx context.Context, factory aria2ControllerFactory) (watch.Aria2Overview, error) {
	if factory == nil {
		return watch.Aria2Overview{}, fmt.Errorf("aria2 controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aria2CommandTimeout)
	defer cancel()
	return factory().Overview(cmdCtx)
}

func runAria2Action(
	ctx context.Context,
	factory aria2ControllerFactory,
	action func(context.Context, *watch.Aria2Controller) (watch.Aria2ActionResult, error),
) (watch.Aria2ActionResult, error) {
	if factory == nil {
		return watch.Aria2ActionResult{}, fmt.Errorf("aria2 controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aria2CommandTimeout)
	defer cancel()
	return action(cmdCtx, factory())
}

func aria2HelpMessage() string {
	return "aria2 管理命令：\n/aria2_overview 查看 TDL 任务总览\n/aria2_pause_all 暂停全部 TDL 任务\n/aria2_start_all 开始全部已暂停的 TDL 任务\n/aria2_retry 重试已停止且未完成的 TDL 任务"
}

func formatAria2Overview(overview watch.Aria2Overview) string {
	var parts []string
	parts = append(parts,
		"TDL aria2 任务总览：",
		fmt.Sprintf("任务总数：%d", overview.TotalTasks),
		fmt.Sprintf("下载中：%d", overview.RemainingTasks),
		fmt.Sprintf("下载剩余：%s", utils.Byte.FormatBinaryBytes(overview.RemainingBytes)),
	)
	if len(overview.StatusCounts) > 0 {
		parts = append(parts, "状态分布："+formatAria2StatusCounts(overview.StatusCounts))
	}
	if len(overview.RetryCandidates) > 0 {
		parts = append(parts,
			fmt.Sprintf("可重试任务：%d", len(overview.RetryCandidates)),
			fmt.Sprintf("可重试剩余量：%s", utils.Byte.FormatBinaryBytes(overview.RetryBytes)),
			"发送 /aria2_retry 重试这些任务。",
		)
	}
	return strings.Join(parts, "\n")
}

func formatAria2ActionResult(action string, result watch.Aria2ActionResult) string {
	parts := []string{
		action + "完成。",
		fmt.Sprintf("匹配任务：%d", result.Matched),
		fmt.Sprintf("成功处理：%d", result.Changed),
		fmt.Sprintf("跳过：%d", result.Skipped),
	}
	if len(result.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("失败：%d", len(result.Errors)))
		for _, line := range firstStrings(result.Errors, 5) {
			parts = append(parts, "- "+line)
		}
	}
	return strings.Join(parts, "\n")
}

func formatAria2StatusCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func firstStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}
