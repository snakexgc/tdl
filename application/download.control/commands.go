package downloadcontrol

import "github.com/snakexgc/tdl/interfaces/types"

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: actionStart, Description: "开始使用并显示下载器控制键盘", Owner: ID},
		{Name: "menu", Description: "显示当前下载器控制键盘", Owner: ID},
		{Name: "help", Description: "查看当前下载器帮助", Owner: ID},
		{Name: "info", Description: "查看当前下载器信息", Owner: ID},
		{Name: "downloads", Description: "查看当前下载器管理命令", Owner: ID, Aliases: []string{"downloads_help", "aria2", "aria2_help", legacyInternalMode, "internal_help"}},
		{Name: "downloads_active", Description: "查看正在下载的任务", Owner: ID, Aliases: []string{"aria2_active", "internal_active"}},
		{Name: "downloads_waiting", Description: "查看等待或暂停的任务", Owner: ID, Aliases: []string{"aria2_waiting", "internal_waiting"}},
		{Name: "downloads_stopped", Description: "查看已完成或停止的任务", Owner: ID, Aliases: []string{"aria2_stopped", "internal_stopped"}},
		{Name: "downloads_overview", Description: "查看下载任务概况", Owner: ID, Aliases: []string{"aria2_overview", "internal_overview"}},
		{Name: "downloads_pause_all", Description: "暂停全部下载任务", Owner: ID, Aliases: []string{"aria2_pause_all", "internal_pause_all"}},
		{Name: "downloads_start_all", Description: "开始全部下载任务", Owner: ID, Aliases: []string{"aria2_start_all", "internal_start_all"}},
		{Name: "aria2_retry", Description: "重试已停止的下载任务", Owner: ID},
	}
}

const actionStart = "start"
