package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/iyear/tdl/app/updater"
	"github.com/iyear/tdl/pkg/config"
)

type tdlUpdateController struct {
	requestUpdate func(updater.Plan)
}

func newTDLUpdateController(requestUpdate func(updater.Plan)) *tdlUpdateController {
	return &tdlUpdateController{requestUpdate: requestUpdate}
}

func handleUpdateCommand(ctx *th.Context, msg *telego.Message, text string, controller *tdlUpdateController) (bool, error) {
	if commandName(text) != "/update_tdl" {
		return false, nil
	}
	confirm := updateCommandConfirmed(text)
	if !confirm {
		checkCtx := context.WithoutCancel(ctx)
		info, err := updater.CheckLatest(checkCtx, config.Get().Proxy)
		if err != nil {
			return true, sendMessage(ctx, msg.Chat.ID, "检查更新失败："+err.Error())
		}
		message := formatUpdateInfo(info)
		if info.NeedsUpdate && info.CanUpdate {
			message += "\n\n确认更新请发送：/update_tdl confirm"
		}
		return true, sendMessage(ctx, msg.Chat.ID, message)
	}

	if controller == nil || controller.requestUpdate == nil {
		return true, sendMessage(ctx, msg.Chat.ID, "当前运行模式不支持自动更新。")
	}
	_ = sendMessage(ctx, msg.Chat.ID, "正在下载更新，请稍候...")
	downloadCtx := context.WithoutCancel(ctx)
	plan, info, err := updater.DownloadLatest(downloadCtx, config.Get().Proxy)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return true, sendMessage(ctx, msg.Chat.ID, "更新已取消。")
		}
		return true, sendMessage(ctx, msg.Chat.ID, "下载更新失败："+err.Error())
	}
	if !info.NeedsUpdate {
		return true, sendMessage(ctx, msg.Chat.ID, "当前已是最新版本，无需更新。")
	}
	if err := sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("更新包已下载，准备更新到 %s 并重启。", info.LatestVersion)); err != nil {
		return true, err
	}
	controller.requestUpdate(plan)
	return true, nil
}

func updateCommandConfirmed(text string) bool {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return false
	}
	arg := strings.ToLower(strings.TrimSpace(fields[1]))
	return arg == "confirm" || arg == "yes" || arg == "y" || arg == "确认"
}

func formatUpdateInfo(info updater.Info) string {
	lines := []string{
		"tdl 更新检查",
		"当前版本：" + emptyDash(info.CurrentVersion),
		"当前提交：" + emptyDash(info.CurrentCommit),
		"运行平台：" + info.GOOS + "/" + info.GOARCH,
		"最新版本：" + emptyDash(info.LatestVersion),
	}
	if info.LatestURL != "" {
		lines = append(lines, "发布地址："+info.LatestURL)
	}
	if info.AssetName != "" {
		lines = append(lines, "更新资产："+info.AssetName)
	}
	if info.Message != "" {
		lines = append(lines, "状态："+info.Message)
	}
	if info.NeedsUpdate && !info.CanUpdate {
		lines = append(lines, "无法自动更新：没有匹配当前平台的发布资产。")
	}
	return strings.Join(lines, "\n")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
