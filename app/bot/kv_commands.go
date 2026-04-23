package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/kv"
)

const cleanKVCommandTimeout = 30 * time.Second

func handleKVCommand(
	ctx *th.Context,
	msg *telego.Message,
	text string,
	engine kv.Storage,
	namespace string,
	namespaceKV storage.Storage,
) (bool, error) {
	cmd, _, _ := tu.ParseCommandPayload(text)
	if "/"+cmd != "/clean_kv" {
		return false, nil
	}

	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanKVCommandTimeout)
	defer cancel()

	result, err := cleanCurrentNamespaceKV(cmdCtx, engine, namespace, namespaceKV)
	if err != nil {
		return true, sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("KV 清理失败：%v", err))
	}
	return true, sendMessage(ctx, msg.Chat.ID, formatCleanKVResult(result))
}
