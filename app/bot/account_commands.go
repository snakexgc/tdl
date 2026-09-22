package bot

import (
	"github.com/go-faster/errors"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func handleAccountCommand(ctx *th.Context, msg *telego.Message, text string, loginMgr ports.BotLogin) (bool, error) {
	fromID, chatID := msg.From.ID, msg.Chat.ID
	switch commandName(text) {
	case "/login_code":
		loginNamespace, err := loginNamespaceFromCommand(text)
		if err != nil {
			sendLoginNamespaceUsage(ctx, chatID, "/login_code")
			return true, nil
		}
		if err := loginMgr.StartCode(fromID, chatID, loginNamespace); err != nil {
			if errors.Is(err, errLoginBusy) {
				_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
					tu.ID(chatID),
					"已有登录流程正在进行，请先完成或发送 /cancel_login 取消。",
				))
				return true, nil
			}
			return true, err
		}
		return true, nil
	case "/cancel_login":
		if loginMgr.Cancel(fromID, chatID) {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "正在取消当前登录流程。"))
			return true, nil
		}
		if loginMgr.Busy() {
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(
				tu.ID(chatID),
				"已有登录流程正在进行，只能由发起会话取消。",
			))
			return true, nil
		}
		_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "当前没有可取消的登录流程。"))
		return true, nil
	}
	return false, nil
}
