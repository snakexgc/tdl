package bot

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/utils"
)

const aria2CommandTimeout = 30 * time.Second

const (
	aria2MenuActive       = "⬇️正在下载"
	aria2MenuWaiting      = "⌛️ 正在等待"
	aria2MenuStopped      = "✅ 已完成/停止"
	aria2MenuPauseTask    = "⏸️暂停任务"
	aria2MenuUnpauseTask  = "▶️恢复任务"
	aria2MenuRemoveTask   = "❌ 删除任务"
	aria2MenuClearStopped = "❌ ❌ 清空已完成/停止"
	aria2MenuClose        = "关闭键盘"
)

const (
	aria2SchemeHTTPS = "https"
	aria2SchemeHTTP  = "http"
	aria2SchemeWSS   = "wss"
	aria2SchemeWS    = "ws"
)

const (
	actionPause = "pause"
)

type aria2ControllerFactory func() *watch.Aria2Controller

func handleAria2Command(ctx *th.Context, msg *telego.Message, text string, factory aria2ControllerFactory) (bool, error) {
	switch text {
	case aria2MenuActive:
		return true, sendAria2TaskList(ctx, msg.Chat.ID, "正在下载的 aria2 任务：", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.ActiveTasks(ctx)
		})
	case aria2MenuWaiting:
		return true, sendAria2TaskList(ctx, msg.Chat.ID, "正在等待/暂停的 aria2 任务：", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.WaitingTasks(ctx)
		})
	case aria2MenuStopped:
		return true, sendAria2TaskList(ctx, msg.Chat.ID, "已完成/停止的 aria2 任务：", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.StoppedTasks(ctx)
		})
	case aria2MenuPauseTask:
		return true, sendAria2TaskButtons(ctx, msg.Chat.ID, "请选择要暂停的任务：", "pause", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.ActiveTasks(ctx)
		})
	case aria2MenuUnpauseTask:
		return true, sendAria2TaskButtons(ctx, msg.Chat.ID, "请选择要恢复的任务：", "unpause", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.WaitingTasks(ctx)
		})
	case aria2MenuRemoveTask:
		return true, sendAria2TaskButtons(ctx, msg.Chat.ID, "请选择要删除的任务：", "remove", factory, listActiveAndWaitingAria2Tasks)
	case aria2MenuClearStopped:
		result, err := runAria2Action(ctx, factory, func(ctx context.Context, controller *watch.Aria2Controller) (watch.Aria2ActionResult, error) {
			return controller.ClearStopped(ctx)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("清空已完成/停止任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2ActionResult("清空已完成/停止任务", result))
	case aria2MenuClose:
		_, err := ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(msg.Chat.ID), "键盘已关闭，发送 /menu 可重新打开。").WithReplyMarkup(tu.ReplyKeyboardRemove()))
		return true, err
	}

	cmd, _, _ := tu.ParseCommandPayload(text)
	switch "/" + cmd {
	case botCmdStart, botCmdMenu:
		return true, sendAria2Menu(ctx, msg.Chat.ID, msg.From.ID)
	case botCmdHelp:
		return true, sendMessage(ctx, msg.Chat.ID, aria2BotHelpMessage(msg.From.ID))
	case botCmdInfo:
		options, err := runAria2GlobalOptions(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取 aria2 设置失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2GlobalOptions(options))
	case botCmdAria2Active, botCmdDownloadsActive:
		return true, sendAria2TaskList(ctx, msg.Chat.ID, "正在下载的 aria2 任务：", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.ActiveTasks(ctx)
		})
	case botCmdAria2Waiting, botCmdDownloadsWaiting:
		return true, sendAria2TaskList(ctx, msg.Chat.ID, "正在等待/暂停的 aria2 任务：", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.WaitingTasks(ctx)
		})
	case botCmdAria2Stopped, botCmdDownloadsStopped:
		return true, sendAria2TaskList(ctx, msg.Chat.ID, "已完成/停止的 aria2 任务：", factory, func(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
			return c.StoppedTasks(ctx)
		})
	case botCmdAria2, botCmdAria2Help, botCmdDownloads, botCmdDownloadsHelp:
		return true, sendMessage(ctx, msg.Chat.ID, aria2HelpMessage())
	case botCmdAria2Overview, botCmdDownloadsOverview:
		overview, err := runAria2Overview(ctx, factory)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("获取 aria2 任务总览失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2Overview(overview))
	case botCmdAria2PauseAll, botCmdDownloadsPauseAll:
		result, err := runAria2Action(ctx, factory, func(ctx context.Context, controller *watch.Aria2Controller) (watch.Aria2ActionResult, error) {
			return controller.PauseAll(ctx)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("暂停 TDL aria2 任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2ActionResult("暂停全部", result))
	case botCmdAria2StartAll, botCmdDownloadsStartAll:
		result, err := runAria2Action(ctx, factory, func(ctx context.Context, controller *watch.Aria2Controller) (watch.Aria2ActionResult, error) {
			return controller.StartAll(ctx)
		})
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("开始 TDL aria2 任务失败：%v", err))
		}
		return true, sendMessage(ctx, msg.Chat.ID, formatAria2ActionResult("开始全部", result))
	case botCmdAria2Retry:
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

func runAria2GlobalOptions(ctx context.Context, factory aria2ControllerFactory) (map[string]string, error) {
	if factory == nil {
		return nil, fmt.Errorf("aria2 controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aria2CommandTimeout)
	defer cancel()
	return factory().GlobalOptions(cmdCtx)
}

func sendAria2Menu(ctx *th.Context, chatID int64, userID int64) error {
	menu := aria2ReplyKeyboard()
	_, err := ctx.Bot().SendMessage(ctx, tu.Message(
		tu.ID(chatID),
		fmt.Sprintf("aria2 控制机器人已就绪。\n您的用户 ID：%d\n\n发送 Telegram 消息链接即可按 watch 流程下载消息中的文件。", userID),
	).WithReplyMarkup(menu))
	return err
}

func aria2ReplyKeyboard() *telego.ReplyKeyboardMarkup {
	menu := tu.Keyboard(
		tu.KeyboardRow(tu.KeyboardButton(aria2MenuActive), tu.KeyboardButton(aria2MenuWaiting), tu.KeyboardButton(aria2MenuStopped)),
		tu.KeyboardRow(tu.KeyboardButton(aria2MenuPauseTask), tu.KeyboardButton(aria2MenuUnpauseTask), tu.KeyboardButton(aria2MenuRemoveTask)),
		tu.KeyboardRow(tu.KeyboardButton(aria2MenuClearStopped), tu.KeyboardButton(aria2MenuClose)),
	)
	menu.ResizeKeyboard = true
	return menu
}

func sendAria2TaskList(
	ctx *th.Context,
	chatID int64,
	title string,
	factory aria2ControllerFactory,
	list func(context.Context, *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error),
) error {
	tasks, err := runAria2TaskList(ctx, factory, list)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取 aria2 任务失败：%v", err))
	}
	if len(tasks) == 0 {
		return sendMessage(ctx, chatID, strings.TrimSuffix(title, "：")+"为空。")
	}
	return sendMessage(ctx, chatID, formatAria2Tasks(title, tasks))
}

func runAria2TaskList(
	ctx context.Context,
	factory aria2ControllerFactory,
	list func(context.Context, *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error),
) ([]watch.Aria2DownloadStatus, error) {
	if factory == nil {
		return nil, fmt.Errorf("aria2 controller is not configured")
	}
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aria2CommandTimeout)
	defer cancel()
	return list(cmdCtx, factory())
}

func sendAria2TaskButtons(
	ctx *th.Context,
	chatID int64,
	title string,
	action string,
	factory aria2ControllerFactory,
	list func(context.Context, *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error),
) error {
	tasks, err := runAria2TaskList(ctx, factory, list)
	if err != nil {
		return sendMessage(ctx, chatID, fmt.Sprintf("获取 aria2 任务失败：%v", err))
	}
	if len(tasks) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的任务。")
	}

	rows := make([][]telego.InlineKeyboardButton, 0, len(tasks))
	for _, task := range tasks {
		if task.GID == "" {
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(telego.InlineKeyboardButton{
			Text:         truncateRunes(watch.Aria2TaskName(task), 56),
			CallbackData: fmt.Sprintf("aria2:%s:%s", action, task.GID),
		}))
	}
	if len(rows) == 0 {
		return sendMessage(ctx, chatID, "当前没有可操作的任务。")
	}

	_, err = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), title).WithReplyMarkup(tu.InlineKeyboard(rows...)))
	return err
}

func listActiveAndWaitingAria2Tasks(ctx context.Context, c *watch.Aria2Controller) ([]watch.Aria2DownloadStatus, error) {
	active, err := c.ActiveTasks(ctx)
	if err != nil {
		return nil, err
	}
	waiting, err := c.WaitingTasks(ctx)
	if err != nil {
		return nil, err
	}
	return append(active, waiting...), nil
}

func isTorrentDocument(doc *telego.Document) bool {
	if doc == nil {
		return false
	}
	if strings.EqualFold(doc.MimeType, "application/x-bittorrent") {
		return true
	}
	return strings.EqualFold(path.Ext(doc.FileName), ".torrent")
}

func aria2HelpMessage() string {
	return strings.Join([]string{
		"aria2 管理命令：",
		"/start 或 /menu 打开控制键盘",
		"/info 查看 aria2 全局设置",
		"/downloads_active 查看正在下载任务",
		"/downloads_waiting 查看等待/暂停任务",
		"/downloads_stopped 查看已完成/停止任务",
		"",
		"TDL 任务命令：",
		"/downloads_overview 查看 TDL 任务总览",
		"/downloads_pause_all 暂停全部 TDL 任务",
		"/downloads_start_all 开始全部已暂停的 TDL 任务",
		"/aria2_retry 重试已停止且未完成的 TDL 任务",
		"发送 Telegram 消息链接，按 watch 流程下载消息中的文件",
	}, "\n")
}

func aria2BotHelpMessage(userID int64) string {
	return fmt.Sprintf("开启菜单：/start 或 /menu\n关闭菜单：点击\"%s\"\n系统信息：/info\n提交下载：发送 Telegram 消息链接\nADMIN_ID：%d", aria2MenuClose, userID)
}

func formatAria2GlobalOptions(options map[string]string) string {
	return strings.Join([]string{
		"aria2 当前设置：",
		"下载目录: " + valueOrUnknown(options["dir"]),
		"最大同时下载数: " + valueOrUnknown(options["max-concurrent-downloads"]),
		"允许覆盖: " + formatAria2Bool(options["allow-overwrite"]),
	}, "\n")
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(未设置)"
	}
	return value
}

func formatAria2Bool(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes":
		return "是"
	case "false", "no":
		return "否"
	default:
		return valueOrUnknown(value)
	}
}

func formatAria2Tasks(title string, tasks []watch.Aria2DownloadStatus) string {
	const limit = 20

	parts := []string{title}
	for i, task := range tasks {
		if i >= limit {
			parts = append(parts, fmt.Sprintf("还有 %d 个任务未显示。", len(tasks)-limit))
			break
		}
		info := watch.Aria2TaskInfoFromStatus(task)
		speed := parseAria2Int(task.DownloadSpeed)
		lines := []string{
			fmt.Sprintf("任务名称: %s", watch.Aria2TaskName(task)),
			fmt.Sprintf("状态: %s", info.Status),
			fmt.Sprintf("进度: %s", formatAria2Progress(info.TotalLength, info.CompletedLength)),
			fmt.Sprintf("大小: %s", formatAria2Size(info.TotalLength)),
			fmt.Sprintf("速度: %s/s", utils.Byte.FormatBinaryBytes(speed)),
		}
		if info.ErrorMessage != "" {
			lines = append(lines, "错误: "+info.ErrorMessage)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func formatAria2Progress(total, completed int64) string {
	if total <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.2f%%", float64(completed)/float64(total)*100)
}

func formatAria2Size(size int64) string {
	if size <= 0 {
		return "(未知)"
	}
	return utils.Byte.FormatBinaryBytes(size)
}

func parseAria2Int(value string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func handleAria2Callback(ctx *th.Context, query telego.CallbackQuery, factory aria2ControllerFactory) error {
	if !strings.HasPrefix(query.Data, "aria2:") {
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
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("aria2 未配置。"))
		return sendMessage(ctx, chatID, "aria2 controller is not configured")
	}

	action, gid := parts[1], parts[2]
	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aria2CommandTimeout)
	defer cancel()
	controller := factory()

	var err error
	var done string
	switch action {
	case actionPause:
		err = controller.PauseTask(cmdCtx, gid)
		done = "暂停成功"
	case "unpause":
		err = controller.UnpauseTask(cmdCtx, gid)
		done = "恢复成功"
	case "remove":
		err = controller.RemoveTask(cmdCtx, gid)
		done = "删除成功"
	default:
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("未知操作。"))
		return nil
	}
	if err != nil {
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("操作失败。"))
		return sendMessage(ctx, chatID, fmt.Sprintf("%s %s 失败：%v", action, gid, err))
	}

	_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(done))
	return sendMessage(ctx, chatID, fmt.Sprintf("%s：%s", done, gid))
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
