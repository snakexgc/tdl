package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/utils"
)

const (
	aria2EventReconnectDelay = 5 * time.Second
	aria2ProgressInterval    = 3 * time.Second
)

type aria2EventBot interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
	EditMessageText(ctx context.Context, params *telego.EditMessageTextParams) (*telego.Message, error)
}

type aria2Event struct {
	Method string            `json:"method"`
	Params []aria2EventParam `json:"params"`
}

type aria2EventParam struct {
	GID string `json:"gid"`
}

func runAria2EventListener(ctx context.Context, bot aria2EventBot, notifier *botNotifier, factory aria2ControllerFactory) {
	if bot == nil || notifier == nil || factory == nil {
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		if !aria2DownloaderEnabled() {
			waitAria2EventReconnect(ctx)
			continue
		}

		wsURL, err := aria2WebSocketURL(config.Get().Aria2.RPCURL)
		if err == nil {
			_ = listenAria2Events(ctx, wsURL, bot, notifier, factory)
		}
		if ctx.Err() != nil {
			return
		}

		if !waitAria2EventReconnect(ctx) {
			return
		}
	}
}

func waitAria2EventReconnect(ctx context.Context) bool {
	timer := time.NewTimer(aria2EventReconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func listenAria2Events(ctx context.Context, wsURL string, bot aria2EventBot, notifier *botNotifier, factory aria2ControllerFactory) error {
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow()

	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var event aria2Event
		if err := json.Unmarshal(payload, &event); err != nil || event.Method == "" || len(event.Params) == 0 {
			continue
		}
		gid := event.Params[0].GID
		if gid == "" {
			continue
		}

		go handleAria2Event(ctx, bot, notifier, factory, event.Method, gid)
	}
}

func handleAria2Event(ctx context.Context, bot aria2EventBot, notifier *botNotifier, factory aria2ControllerFactory, method, gid string) {
	if !aria2DownloaderEnabled() {
		return
	}

	cmdCtx, cancel := context.WithTimeout(ctx, aria2CommandTimeout)
	defer cancel()

	controller := factory()
	task, err := controller.TellStatus(cmdCtx, gid)
	if err != nil {
		notifier.Notify(ctx, fmt.Sprintf("读取 aria2 任务 %s 状态失败：%v", gid, err))
		return
	}

	switch method {
	case "aria2.onDownloadStart":
		notifyAria2DownloadStart(ctx, bot, notifier, factory, task)
	case "aria2.onDownloadComplete":
		notifyAria2DownloadComplete(ctx, notifier, task)
	case "aria2.onDownloadError":
		notifyAria2DownloadError(ctx, notifier, task)
	case "aria2.onDownloadPause":
		notifier.Notify(ctx, fmt.Sprintf("%s 任务已经成功暂停", watch.Aria2TaskName(task)))
	}
}

func notifyAria2DownloadStart(ctx context.Context, bot aria2EventBot, notifier *botNotifier, factory aria2ControllerFactory, task watch.Aria2DownloadStatus) {
	for _, chatID := range notifier.ChatIDs() {
		msg, err := bot.SendMessage(ctx, tu.Message(
			tu.ID(chatID),
			fmt.Sprintf("%s 任务已经开始下载...\n对应路径: %s", watch.Aria2TaskName(task), valueOrUnknown(task.Dir)),
		))
		if err != nil || msg == nil {
			continue
		}
		go pollAria2DownloadProgress(ctx, bot, chatID, msg.MessageID, factory, task.GID)
	}
}

func pollAria2DownloadProgress(ctx context.Context, bot aria2EventBot, chatID int64, messageID int, factory aria2ControllerFactory, gid string) {
	ticker := time.NewTicker(aria2ProgressInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cmdCtx, cancel := context.WithTimeout(ctx, aria2CommandTimeout)
		task, err := factory().TellStatus(cmdCtx, gid)
		cancel()
		if err != nil {
			return
		}

		info := watch.Aria2TaskInfoFromStatus(task)
		if info.Status != "active" && info.Status != "waiting" {
			return
		}

		_, _ = bot.EditMessageText(ctx, tu.EditMessageText(
			tu.ID(chatID),
			messageID,
			fmt.Sprintf("%s 下载中...\n对应路径: %s\n进度: %s\n大小: %s\n速度: %s/s\n时间: %s",
				watch.Aria2TaskName(task),
				valueOrUnknown(task.Dir),
				formatAria2Progress(info.TotalLength, info.CompletedLength),
				formatAria2Size(info.TotalLength),
				utils.Byte.FormatBinaryBytes(parseAria2Int(task.DownloadSpeed)),
				time.Now().Format("2006-01-02 15:04:05"),
			),
		))
	}
}

func notifyAria2DownloadComplete(ctx context.Context, notifier *botNotifier, task watch.Aria2DownloadStatus) {
	if len(task.Files) == 0 {
		notifier.Notify(ctx, fmt.Sprintf("下载完成===> %s", watch.Aria2TaskName(task)))
		return
	}

	for _, file := range task.Files {
		filePath := strings.TrimSpace(file.Path)
		if filePath == "" {
			continue
		}
		notifier.Notify(ctx, "下载完成===> "+filePath)
	}
}

func notifyAria2DownloadError(ctx context.Context, notifier *botNotifier, task watch.Aria2DownloadStatus) {
	if task.ErrorCode == "12" {
		notifier.Notify(ctx, "任务已经在下载，可以删除任务后重新添加")
		return
	}
	if task.ErrorMessage != "" {
		notifier.Notify(ctx, task.ErrorMessage)
		return
	}
	notifier.Notify(ctx, fmt.Sprintf("%s 下载失败", watch.Aria2TaskName(task)))
}

func aria2WebSocketURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("aria2 rpc_url is empty")
	}

	if !strings.Contains(raw, "://") {
		return "ws://" + raw, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case aria2SchemeHTTP:
		parsed.Scheme = aria2SchemeWS
	case aria2SchemeHTTPS:
		parsed.Scheme = aria2SchemeWSS
	case aria2SchemeWS, aria2SchemeWSS:
	default:
		return "", fmt.Errorf("unsupported aria2 rpc scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}
