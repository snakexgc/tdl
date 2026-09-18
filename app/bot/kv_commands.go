package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const cleanKVCommandTimeout = 30 * time.Second

func handleKVCommand(
	ctx *th.Context,
	msg *telego.Message,
	text string,
	maintenance ports.KVMaintenance,
	account types.AccountID,
) (bool, error) {
	cmd, _, _ := tu.ParseCommandPayload(text)
	if "/"+cmd != "/clean_kv" {
		return false, nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, cleanKVCommandTimeout)
	defer cancel()

	result, err := maintenance.Clean(cmdCtx, account)
	if err != nil {
		return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("KV 清理失败：%v", err))
	}
	return true, sendMessage(ctx, msg.Chat.ID, formatCleanKVResult(result))
}
