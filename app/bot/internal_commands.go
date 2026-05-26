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

const internalCommandTimeout = 30 * time.Second

type internalDownloadControllerFactory func() *watch.InternalDownloadController

func handleInternalDownloadCommand(
	ctx *th.Context,
	msg *telego.Message,
	text string,
	factory internalDownloadControllerFactory,
) (bool, error) {
	switch text {
	case aria2MenuActive:
		return true, sendInternalDownloadList(ctx, msg.Chat.ID, "内部下载器正在下载的任务：", factory, filterInternalDownloads(watch.InternalDownloadStatusActive))
	case aria2MenuWaiting:
		return true, sendInternalDownloadList(ctx, msg.Chat.ID, "内部下载器正在等待/暂停的任务：", factory, filterInternalDownloads(watch.InternalDownloadStatusQueued, watch.InternalDownloadStatusPaused))
	case aria2MenuStopped:
		return true, sendInternalDownloadList(ctx, msg.Chat.ID, "内部下载器已完成/停止的任务：", factory, filterInternalDownloads(watch.InternalDownloadStatusComplete, watch.InternalDownloadStatusError, watch.InternalDownloadStatusRemoved))
	case aria2MenuPauseTask:
		return true, sendInternalDownloadButtons(ctx, msg.Chat.ID, "请选择要暂停的内部下载任务：", "pause", factory, filterInternalDownloads(watch.InternalDownloadStatusActive, watch.InternalDownloadStatusQueued, watch.InternalDownloadStatusError))
	case aria2MenuUnpauseTask:
		return true, sendInternalDownloadButtons(ctx, msg.Chat.ID, "请选择要恢复的内部下载任务：", "start", factory, filterInternalDownloads(watch.InternalDownloadStatusPaused, watch.InternalDownloadStatusError))
	case aria2MenuRemoveTask:
		return true, sendInternalDownloadButtons(ctx, msg.Chat.ID, "请选择要删除的内部下载任务：", "delete", factory, filterInternalDownloads(watch.InternalDownloadStatusActive, watch.InternalDownloadStatusQueued, watch.InternalDownloadStatusPaused, watch.InternalDownloadStatusError))
	case aria2MenuClearStopped:
		items, err := runInternalDownloadList(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取内部下载任务失败：%v", err))
		}
		ids := internalDownloadIDs(filterInternalDownloads(watch.InternalDownloadStatusComplete, watch.InternalDownloadStatusError, watch.InternalDownloadStatusRemoved)(items))
		if len(ids) == 0 {
			return true, sendMessage(ctx, msg.Chat.ID, "当前没有已完成/停止的内部下载任务。")
		}
		result, err := runInternalDownloadAction(ctx, factory, func(ctx context.Context, controller *watch.InternalDownloadController) (watch.InternalDownloadActionResult, error) {
			return controller.Delete(ctx, ids)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("清空内部下载已完成/停止任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatInternalDownloadActionResult("清空已完成/停止任务", result))
	case aria2MenuClose:
		_, err := ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(msg.Chat.ID), "键盘已关闭，发送 /menu 可重新打开。").WithReplyMarkup(tu.ReplyKeyboardRemove()))
		return true, err
	}

	cmd, _, _ := tu.ParseCommandPayload(text)
	switch "/" + cmd {
	case botCmdStart, botCmdMenu:
		return true, sendInternalDownloadMenu(ctx, msg.Chat.ID, msg.From.ID)
	case botCmdHelp:
		return true, sendMessage(ctx, msg.Chat.ID, internalDownloadBotHelpMessage(msg.From.ID))
	case botCmdInfo, botCmdDownloadsOverview, botCmdInternalOverview:
		items, err := runInternalDownloadList(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取内部下载任务总览失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatInternalDownloadOverview(items))
	case botCmdDownloads, botCmdDownloadsHelp, botCmdInternal, botCmdInternalHelp:
		return true, sendMessage(ctx, msg.Chat.ID, internalDownloadHelpMessage())
	case botCmdDownloadsActive, botCmdInternalActive:
		return true, sendInternalDownloadList(ctx, msg.Chat.ID, "内部下载器正在下载的任务：", factory, filterInternalDownloads(watch.InternalDownloadStatusActive))
	case botCmdDownloadsWaiting, botCmdInternalWaiting:
		return true, sendInternalDownloadList(ctx, msg.Chat.ID, "内部下载器正在等待/暂停的任务：", factory, filterInternalDownloads(watch.InternalDownloadStatusQueued, watch.InternalDownloadStatusPaused))
	case botCmdDownloadsStopped, botCmdInternalStopped:
		return true, sendInternalDownloadList(ctx, msg.Chat.ID, "内部下载器已完成/停止的任务：", factory, filterInternalDownloads(watch.InternalDownloadStatusComplete, watch.InternalDownloadStatusError, watch.InternalDownloadStatusRemoved))
	case botCmdDownloadsPauseAll, botCmdInternalPauseAll:
		return true, runInternalDownloadBulkCommand(ctx, msg.Chat.ID, "暂停全部", factory, filterInternalDownloads(watch.InternalDownloadStatusActive, watch.InternalDownloadStatusQueued, watch.InternalDownloadStatusError), func(ctx context.Context, controller *watch.InternalDownloadController, ids []string) (watch.InternalDownloadActionResult, error) {
			return controller.Pause(ctx, ids)
		})
	case botCmdDownloadsStartAll, botCmdInternalStartAll:
		return true, runInternalDownloadBulkCommand(ctx, msg.Chat.ID, "开始全部", factory, filterInternalDownloads(watch.InternalDownloadStatusPaused, watch.InternalDownloadStatusError), func(ctx context.Context, controller *watch.InternalDownloadController, ids []string) (watch.InternalDownloadActionResult, error) {
			return controller.Start(ctx, ids)
		})
	case botCmdAria2, botCmdAria2Help, botCmdAria2Active, botCmdAria2Waiting, botCmdAria2Stopped, botCmdAria2Overview, botCmdAria2PauseAll, botCmdAria2StartAll, botCmdAria2Retry:
		return true, sendMessage(ctx, msg.Chat.ID, "当前 downloader.mode=internal，请使用 /downloads 或 /menu 管理内部下载器。")
	default:
		return false, nil
	}
}

func sendInternalDownloadMenu(ctx *th.Context, chatID int64, userID int64) error {
	_, err := ctx.Bot().SendMessage(ctx, tu.Message(
		tu.ID(chatID),
		fmt.Sprintf("内部下载器控制面板已就绪。\n您的用户 ID：%d\n\n这里可查看、暂停、恢复和删除 watch 创建的内部下载任务；发送 Telegram 消息链接可直接按 watch 流程提交下载。", userID),
	).WithReplyMarkup(aria2ReplyKeyboard()))
	return err
}

func internalDownloadHelpMessage() string {
	return strings.Join([]string{
		"内部下载器管理命令：",
		"/start 或 /menu 打开控制键盘",
		"/info 查看内部下载任务总览",
		"/downloads_active 查看正在下载任务",
		"/downloads_waiting 查看等待/暂停任务",
		"/downloads_stopped 查看已完成/停止任务",
		"/downloads_pause_all 暂停全部未完成任务",
		"/downloads_start_all 开始全部已暂停/错误任务",
		"发送 Telegram 消息链接，按 watch 流程下载消息中的文件",
	}, "\n")
}

func internalDownloadBotHelpMessage(userID int64) string {
	return fmt.Sprintf("开启菜单：/start 或 /menu\n关闭菜单：点击“%s”\n任务总览：/info\n提交下载：发送 Telegram 消息链接\n当前下载器：internal\nADMIN_ID：%d", aria2MenuClose, userID)
}

func runInternalDownloadList(ctx context.Context, factory internalDownloadControllerFactory) ([]watch.InternalDownloadInfo, error) {
	if factory == nil {
		return nil, fmt.Errorf("internal download controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), internalCommandTimeout)
	defer cancel()
	return factory().List(cmdCtx)
}

func runInternalDownloadAction(
	ctx context.Context,
	factory internalDownloadControllerFactory,
	action func(context.Context, *watch.InternalDownloadController) (watch.InternalDownloadActionResult, error),
) (watch.InternalDownloadActionResult, error) {
	if factory == nil {
		return watch.InternalDownloadActionResult{}, fmt.Errorf("internal download controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), internalCommandTimeout)
	defer cancel()
	return action(cmdCtx, factory())
}

func sendInternalDownloadList(
	ctx *th.Context,
	chatID int64,
	title string,
	factory internalDownloadControllerFactory,
	filter func([]watch.InternalDownloadInfo) []watch.InternalDownloadInfo,
) error {
	items, err := runInternalDownloadList(ctx, factory)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取内部下载任务失败：%v", err))
	}
	items = filter(items)
	if len(items) == 0 {
		return sendMessage(ctx, chatID, strings.TrimSuffix(title, "：")+"为空。")
	}
	return sendMessage(ctx, chatID, formatInternalDownloads(title, items))
}

func sendInternalDownloadButtons(
	ctx *th.Context,
	chatID int64,
	title string,
	action string,
	factory internalDownloadControllerFactory,
	filter func([]watch.InternalDownloadInfo) []watch.InternalDownloadInfo,
) error {
	items, err := runInternalDownloadList(ctx, factory)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取内部下载任务失败：%v", err))
	}
	items = filter(items)
	if len(items) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的内部下载任务。")
	}

	rows := make([][]telego.InlineKeyboardButton, 0, len(items))
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(telego.InlineKeyboardButton{
			Text:         truncateRunes(internalDownloadName(item), 56),
			CallbackData: fmt.Sprintf("internal:%s:%s", action, item.ID),
		}))
	}
	if len(rows) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的内部下载任务。")
	}

	_, err = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), title).WithReplyMarkup(tu.InlineKeyboard(rows...)))
	return err
}

func runInternalDownloadBulkCommand(
	ctx *th.Context,
	chatID int64,
	actionName string,
	factory internalDownloadControllerFactory,
	filter func([]watch.InternalDownloadInfo) []watch.InternalDownloadInfo,
	action func(context.Context, *watch.InternalDownloadController, []string) (watch.InternalDownloadActionResult, error),
) error {
	items, err := runInternalDownloadList(ctx, factory)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取内部下载任务失败：%v", err))
	}
	ids := internalDownloadIDs(filter(items))
	if len(ids) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的内部下载任务。")
	}
	result, err := runInternalDownloadAction(ctx, factory, func(ctx context.Context, controller *watch.InternalDownloadController) (watch.InternalDownloadActionResult, error) {
		return action(ctx, controller, ids)
	})
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("%s内部下载任务失败：%v", actionName, err))
	}
	return sendMessage(ctx, chatID, formatInternalDownloadActionResult(actionName, result))
}

func handleInternalDownloadCallback(ctx *th.Context, query telego.CallbackQuery, factory internalDownloadControllerFactory) error {
	if !strings.HasPrefix(query.Data, "internal:") {
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
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("内部下载器未配置。"))
		return sendMessage(ctx, chatID, "internal download controller is not configured")
	}

	action, id := parts[1], parts[2]
	var done string
	result, err := runInternalDownloadAction(ctx, factory, func(ctx context.Context, controller *watch.InternalDownloadController) (watch.InternalDownloadActionResult, error) {
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
			return watch.InternalDownloadActionResult{}, fmt.Errorf("unknown action: %s", action)
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
	return sendMessage(ctx, chatID, fmt.Sprintf("%s：%s\n%s", done, id, formatInternalDownloadActionResult("操作结果", result)))
}

func filterInternalDownloads(statuses ...string) func([]watch.InternalDownloadInfo) []watch.InternalDownloadInfo {
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	return func(items []watch.InternalDownloadInfo) []watch.InternalDownloadInfo {
		filtered := make([]watch.InternalDownloadInfo, 0, len(items))
		for _, item := range items {
			status := item.Status
			if status == "" {
				status = watch.InternalDownloadStatusQueued
			}
			if _, ok := allowed[status]; ok {
				filtered = append(filtered, item)
			}
		}
		return filtered
	}
}

func internalDownloadIDs(items []watch.InternalDownloadInfo) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func formatInternalDownloads(title string, items []watch.InternalDownloadInfo) string {
	const limit = 20

	parts := []string{title}
	for i, item := range items {
		if i >= limit {
			parts = append(parts, fmt.Sprintf("还有 %d 个任务未显示。", len(items)-limit))
			break
		}
		lines := []string{
			fmt.Sprintf("任务名称: %s", internalDownloadName(item)),
			fmt.Sprintf("状态: %s", formatInternalDownloadStatus(item.Status)),
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

func formatInternalDownloadOverview(items []watch.InternalDownloadInfo) string {
	counts := map[string]int{}
	var remainingBytes int64
	for _, item := range items {
		status := item.Status
		if status == "" {
			status = watch.InternalDownloadStatusQueued
		}
		counts[status]++
		if status != watch.InternalDownloadStatusComplete && status != watch.InternalDownloadStatusRemoved {
			remaining := item.Total - item.Completed
			if remaining > 0 {
				remainingBytes += remaining
			}
		}
	}

	return strings.Join([]string{
		"内部下载器任务总览：",
		fmt.Sprintf("任务总数：%d", len(items)),
		fmt.Sprintf("剩余下载量：%s", utils.Byte.FormatBinaryBytes(remainingBytes)),
		"状态分布：" + formatInternalDownloadStatusCounts(counts),
	}, "\n")
}

func formatInternalDownloadActionResult(action string, result watch.InternalDownloadActionResult) string {
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

func formatInternalDownloadStatusCounts(counts map[string]int) string {
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
		parts = append(parts, fmt.Sprintf("%s=%d", formatInternalDownloadStatus(key), counts[key]))
	}
	return strings.Join(parts, ", ")
}

func formatInternalDownloadStatus(status string) string {
	switch status {
	case "", watch.InternalDownloadStatusQueued:
		return "等待中"
	case watch.InternalDownloadStatusActive:
		return "下载中"
	case watch.InternalDownloadStatusPaused:
		return "已暂停"
	case watch.InternalDownloadStatusComplete:
		return "已完成"
	case watch.InternalDownloadStatusError:
		return "错误"
	case watch.InternalDownloadStatusRemoved:
		return "已移除"
	default:
		return status
	}
}

func internalDownloadName(item watch.InternalDownloadInfo) string {
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
