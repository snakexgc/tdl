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

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/utils"
)

const (
	localStatusActive   = "active"
	localStatusQueued   = "queued"
	localStatusPaused   = "paused"
	localStatusComplete = "complete"
	localStatusError    = "error"
	localStatusRemoved  = "removed"
)

const localCommandTimeout = 30 * time.Second

type localDownloadControllerFactory func() *localDownloadControl

func handleLocalDownloadCommand(
	ctx *th.Context,
	msg *telego.Message,
	text string,
	factory localDownloadControllerFactory,
) (bool, error) {
	switch text {
	case aria2MenuActive:
		return true, sendLocalDownloadList(ctx, msg.Chat.ID, "本地下载器正在下载的任务：", factory, filterLocalDownloads(localStatusActive))
	case aria2MenuWaiting:
		return true, sendLocalDownloadList(ctx, msg.Chat.ID, "本地下载器正在等待/暂停的任务：", factory, filterLocalDownloads(localStatusQueued, localStatusPaused))
	case aria2MenuStopped:
		return true, sendLocalDownloadList(ctx, msg.Chat.ID, "本地下载器已完成/停止的任务：", factory, filterLocalDownloads(localStatusComplete, localStatusError, localStatusRemoved))
	case aria2MenuPauseTask:
		return true, sendLocalDownloadButtons(ctx, msg.Chat.ID, "请选择要暂停的本地下载任务：", "pause", factory, filterLocalDownloads(localStatusActive, localStatusQueued, localStatusError))
	case aria2MenuUnpauseTask:
		return true, sendLocalDownloadButtons(ctx, msg.Chat.ID, "请选择要恢复的本地下载任务：", "start", factory, filterLocalDownloads(localStatusPaused, localStatusError))
	case aria2MenuRemoveTask:
		return true, sendLocalDownloadButtons(ctx, msg.Chat.ID, "请选择要删除的本地下载任务：", "delete", factory, filterLocalDownloads(localStatusActive, localStatusQueued, localStatusPaused, localStatusError))
	case aria2MenuClearStopped:
		items, err := runLocalDownloadList(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取本地下载任务失败：%v", err))
		}
		ids := localDownloadIDs(filterLocalDownloads(localStatusComplete, localStatusError, localStatusRemoved)(items))
		if len(ids) == 0 {
			return true, sendMessage(ctx, msg.Chat.ID, "当前没有已完成/停止的本地下载任务。")
		}
		result, err := runLocalDownloadAction(ctx, factory, func(ctx context.Context, controller *localDownloadControl) (types.DownloadActionResult, error) {
			return controller.Delete(ctx, ids)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("清空本地下载已完成/停止任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatLocalDownloadActionResult("清空已完成/停止任务", result))
	case aria2MenuClose:
		_, err := ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(msg.Chat.ID), "键盘已关闭，发送 /menu 可重新打开。").WithReplyMarkup(tu.ReplyKeyboardRemove()))
		return true, err
	}

	cmd, _, _ := tu.ParseCommandPayload(text)
	switch "/" + cmd {
	case botCmdStart, botCmdMenu:
		return true, sendLocalDownloadMenu(ctx, msg.Chat.ID, msg.From.ID)
	case botCmdHelp:
		return true, sendMessage(ctx, msg.Chat.ID, localDownloadBotHelpMessage(msg.From.ID))
	case botCmdInfo, botCmdDownloadsOverview:
		items, err := runLocalDownloadList(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取本地下载任务总览失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatLocalDownloadOverview(items))
	case botCmdDownloads:
		return true, sendMessage(ctx, msg.Chat.ID, localDownloadHelpMessage())
	case botCmdDownloadsActive:
		return true, sendLocalDownloadList(ctx, msg.Chat.ID, "本地下载器正在下载的任务：", factory, filterLocalDownloads(localStatusActive))
	case botCmdDownloadsWaiting:
		return true, sendLocalDownloadList(ctx, msg.Chat.ID, "本地下载器正在等待/暂停的任务：", factory, filterLocalDownloads(localStatusQueued, localStatusPaused))
	case botCmdDownloadsStopped:
		return true, sendLocalDownloadList(ctx, msg.Chat.ID, "本地下载器已完成/停止的任务：", factory, filterLocalDownloads(localStatusComplete, localStatusError, localStatusRemoved))
	case botCmdDownloadsPauseAll:
		return true, runLocalDownloadBulkCommand(ctx, msg.Chat.ID, "暂停全部", factory, filterLocalDownloads(localStatusActive, localStatusQueued, localStatusError), func(ctx context.Context, controller *localDownloadControl, ids []string) (types.DownloadActionResult, error) {
			return controller.Pause(ctx, ids)
		})
	case botCmdDownloadsStartAll:
		return true, runLocalDownloadBulkCommand(ctx, msg.Chat.ID, "开始全部", factory, filterLocalDownloads(localStatusPaused, localStatusError), func(ctx context.Context, controller *localDownloadControl, ids []string) (types.DownloadActionResult, error) {
			return controller.Start(ctx, ids)
		})
	case botCmdAria2Retry:
		return true, sendMessage(ctx, msg.Chat.ID, "当前使用本地下载器，请使用 /downloads 或 /menu 管理本地下载器。")
	default:
		return false, nil
	}
}

func sendLocalDownloadMenu(ctx *th.Context, chatID int64, userID int64) error {
	_, err := ctx.Bot().SendMessage(ctx, tu.Message(
		tu.ID(chatID),
		fmt.Sprintf("本地下载器控制面板已就绪。\n您的用户 ID：%d\n\n这里可查看、暂停、恢复和删除 watch 创建的本地下载任务；发送 Telegram 消息链接可直接按 watch 流程提交下载。", userID),
	).WithReplyMarkup(aria2ReplyKeyboard()))
	return err
}

func localDownloadHelpMessage() string {
	return strings.Join([]string{
		"本地下载器管理命令：",
		"/start 或 /menu 打开控制键盘",
		"/info 查看本地下载任务总览",
		"/downloads_active 查看正在下载任务",
		"/downloads_waiting 查看等待/暂停任务",
		"/downloads_stopped 查看已完成/停止任务",
		"/downloads_pause_all 暂停全部未完成任务",
		"/downloads_start_all 开始全部已暂停/错误任务",
		"发送 Telegram 消息链接，按 watch 流程下载消息中的文件",
	}, "\n")
}

func localDownloadBotHelpMessage(userID int64) string {
	return fmt.Sprintf("开启菜单：/start 或 /menu\n关闭菜单：点击“%s”\n任务总览：/info\n提交下载：发送 Telegram 消息链接\n当前下载器：local\nADMIN_ID：%d", aria2MenuClose, userID)
}

func runLocalDownloadList(ctx context.Context, factory localDownloadControllerFactory) ([]types.DownloadTask, error) {
	if factory == nil {
		return nil, fmt.Errorf("local download controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, localCommandTimeout)
	defer cancel()
	return factory().List(cmdCtx)
}

func runLocalDownloadAction(
	ctx context.Context,
	factory localDownloadControllerFactory,
	action func(context.Context, *localDownloadControl) (types.DownloadActionResult, error),
) (types.DownloadActionResult, error) {
	if factory == nil {
		return types.DownloadActionResult{}, fmt.Errorf("local download controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, localCommandTimeout)
	defer cancel()
	return action(cmdCtx, factory())
}

func sendLocalDownloadList(
	ctx *th.Context,
	chatID int64,
	title string,
	factory localDownloadControllerFactory,
	filter func([]types.DownloadTask) []types.DownloadTask,
) error {
	items, err := runLocalDownloadList(ctx, factory)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取本地下载任务失败：%v", err))
	}
	items = filter(items)
	if len(items) == 0 {
		return sendMessage(ctx, chatID, strings.TrimSuffix(title, "：")+"为空。")
	}
	return sendMessage(ctx, chatID, formatLocalDownloads(title, items))
}

func sendLocalDownloadButtons(
	ctx *th.Context,
	chatID int64,
	title string,
	action string,
	factory localDownloadControllerFactory,
	filter func([]types.DownloadTask) []types.DownloadTask,
) error {
	items, err := runLocalDownloadList(ctx, factory)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取本地下载任务失败：%v", err))
	}
	items = filter(items)
	if len(items) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的本地下载任务。")
	}

	rows := make([][]telego.InlineKeyboardButton, 0, len(items))
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(telego.InlineKeyboardButton{
			Text:         truncateRunes(localDownloadName(item), 56),
			CallbackData: fmt.Sprintf("local:%s:%s", action, item.ID),
		}))
	}
	if len(rows) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的本地下载任务。")
	}

	_, err = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), title).WithReplyMarkup(tu.InlineKeyboard(rows...)))
	return err
}

func runLocalDownloadBulkCommand(
	ctx *th.Context,
	chatID int64,
	actionName string,
	factory localDownloadControllerFactory,
	filter func([]types.DownloadTask) []types.DownloadTask,
	action func(context.Context, *localDownloadControl, []string) (types.DownloadActionResult, error),
) error {
	items, err := runLocalDownloadList(ctx, factory)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取本地下载任务失败：%v", err))
	}
	ids := localDownloadIDs(filter(items))
	if len(ids) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的本地下载任务。")
	}
	result, err := runLocalDownloadAction(ctx, factory, func(ctx context.Context, controller *localDownloadControl) (types.DownloadActionResult, error) {
		return action(ctx, controller, ids)
	})
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("%s本地下载任务失败：%v", actionName, err))
	}
	return sendMessage(ctx, chatID, formatLocalDownloadActionResult(actionName, result))
}

func handleLocalDownloadCallback(ctx *th.Context, query telego.CallbackQuery, factory localDownloadControllerFactory) error {
	if !strings.HasPrefix(query.Data, "local:") {
		return nil
	}
	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) != 3 || parts[2] == "" {
		return ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("回调数据无效。"))
	}

	chatID := query.From.ID
	if query.Message != nil {
		chatID = query.Message.GetChat().ID
	}
	if factory == nil {
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("本地下载器未配置。"))
		return sendMessage(ctx, chatID, "local download controller is not configured")
	}

	action, id := parts[1], parts[2]
	var done string
	result, err := runLocalDownloadAction(ctx, factory, func(ctx context.Context, controller *localDownloadControl) (types.DownloadActionResult, error) {
		switch action {
		case actionPause:
			done = "暂停成功"
			return controller.Pause(ctx, []string{id})
		case "start":
			done = "恢复成功"
			return controller.Start(ctx, []string{id})
		case "delete":
			done = "删除成功"
			return controller.Delete(ctx, []string{id})
		default:
			return types.DownloadActionResult{}, fmt.Errorf("unknown action: %s", action)
		}
	})
	if err != nil {
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("操作失败。"))
		return sendMessage(ctx, chatID, fmt.Sprintf("%s %s 失败：%v", action, id, err))
	}
	if result.Changed == 0 {
		done = "未处理"
	}

	_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(done))
	return sendMessage(ctx, chatID, fmt.Sprintf("%s：%s\n%s", done, id, formatLocalDownloadActionResult("操作结果", result)))
}

func filterLocalDownloads(statuses ...string) func([]types.DownloadTask) []types.DownloadTask {
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	return func(items []types.DownloadTask) []types.DownloadTask {
		filtered := make([]types.DownloadTask, 0, len(items))
		for _, item := range items {
			status := item.Status
			if status == "" {
				status = localStatusQueued
			}
			if _, ok := allowed[status]; ok {
				filtered = append(filtered, item)
			}
		}
		return filtered
	}
}

func localDownloadIDs(items []types.DownloadTask) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func formatLocalDownloads(title string, items []types.DownloadTask) string {
	const limit = 20

	parts := []string{title}
	for i, item := range items {
		if i >= limit {
			parts = append(parts, fmt.Sprintf("还有 %d 个任务未显示。", len(items)-limit))
			break
		}
		lines := []string{
			fmt.Sprintf("任务名称: %s", localDownloadName(item)),
			fmt.Sprintf("状态: %s", formatLocalDownloadStatus(item.Status)),
			fmt.Sprintf("进度: %s", formatAria2Progress(item.Total, item.Completed)),
			fmt.Sprintf("大小: %s", formatAria2Size(item.Total)),
			fmt.Sprintf("路径: %s", valueOrUnknown(item.Path)),
		}
		if item.Error != "" {
			lines = append(lines, "错误: "+item.Error)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func formatLocalDownloadOverview(items []types.DownloadTask) string {
	counts := map[string]int{}
	var remainingBytes int64
	for _, item := range items {
		status := item.Status
		if status == "" {
			status = localStatusQueued
		}
		counts[status]++
		if status != localStatusComplete && status != localStatusRemoved {
			remaining := item.Total - item.Completed
			if remaining > 0 {
				remainingBytes += remaining
			}
		}
	}

	return strings.Join([]string{
		"本地下载器任务总览：",
		fmt.Sprintf("任务总数：%d", len(items)),
		fmt.Sprintf("剩余下载量：%s", utils.Byte.FormatBinaryBytes(remainingBytes)),
		"状态分布：" + formatLocalDownloadStatusCounts(counts),
	}, "\n")
}

func formatLocalDownloadActionResult(action string, result types.DownloadActionResult) string {
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

func formatLocalDownloadStatusCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "(无)"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", formatLocalDownloadStatus(key), counts[key]))
	}
	return strings.Join(parts, ", ")
}

func formatLocalDownloadStatus(status string) string {
	switch status {
	case "", localStatusQueued:
		return "等待中"
	case localStatusActive:
		return "下载中"
	case localStatusPaused:
		return "已暂停"
	case localStatusComplete:
		return "已完成"
	case localStatusError:
		return "错误"
	case localStatusRemoved:
		return "已移除"
	default:
		return status
	}
}

func localDownloadName(item types.DownloadTask) string {
	switch {
	case strings.TrimSpace(item.FileName) != "":
		return strings.TrimSpace(item.FileName)
	case strings.TrimSpace(item.Out) != "":
		return strings.TrimSpace(item.Out)
	case strings.TrimSpace(item.TaskID) != "":
		return strings.TrimSpace(item.TaskID)
	default:
		return strings.TrimSpace(item.ID)
	}
}
