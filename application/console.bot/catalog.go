package consolebot

import "github.com/snakexgc/tdl/interfaces/types"

func commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: "start", Description: "开始使用并显示下载器控制键盘"},
		{Name: "menu", Description: "显示当前下载器控制键盘"},
		{Name: "help", Description: "查看当前下载器帮助"},
		{Name: "info", Description: "查看当前下载器信息"},
		{Name: "login_code", Description: "验证码登录（需填写用户名）"},
		{Name: "cancel_login", Description: "取消正在进行的登录"},
		{Name: "forward", Description: "回复 Telegram 消息链接并转发"},
		{Name: "downloads", Description: "查看当前下载器管理命令"},
		{Name: "downloads_active", Description: "查看正在下载的任务"},
		{Name: "downloads_waiting", Description: "查看等待或暂停的任务"},
		{Name: "downloads_stopped", Description: "查看已完成或停止的任务"},
		{Name: "downloads_overview", Description: "查看下载任务概况"},
		{Name: "downloads_pause_all", Description: "暂停全部下载任务"},
		{Name: "downloads_start_all", Description: "开始全部下载任务"},
		{Name: "aria2_retry", Description: "重试已停止的下载任务"},
		{Name: "reboot", Description: "重启(不推荐)"},
		{Name: "update_tdl", Description: "检查并更新 tdl"},
		{Name: "clean_kv", Description: "清空KV缓存(危险)"},
	}
}

func privateCommand(name string) bool {
	for _, command := range commands() {
		if command.Name == name {
			return true
		}
	}
	for _, prefix := range []string{"downloads", "aria2", "internal"} {
		for _, suffix := range []string{"", "_help", "_active", "_waiting", "_stopped", "_overview", "_pause_all", "_start_all"} {
			if name == prefix+suffix {
				return true
			}
		}
	}
	return false
}
