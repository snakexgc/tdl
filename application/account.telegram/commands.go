package accounttelegram

import "github.com/snakexgc/tdl/interfaces/types"

func Commands() []types.ConsoleCommand {
	return []types.ConsoleCommand{
		{Name: "login_code", Description: "验证码登录（需填写用户名）", Owner: ID},
		{Name: "cancel_login", Description: "取消正在进行的登录", Owner: ID},
	}
}
