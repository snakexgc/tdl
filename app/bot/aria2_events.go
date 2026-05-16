package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
)

const (
	aria2EventReconnectDelay = 3 * time.Second
)

const aria2EventDownloadComplete = "aria2.onDownloadComplete"

type aria2Event struct {
	Method string            `json:"method"`
	Params []aria2EventParam `json:"params"`
}

type aria2EventParam struct {
	GID string `json:"gid"`
}

func runAria2EventListener(ctx context.Context, notifier *botNotifier, factory aria2ControllerFactory) {
	if notifier == nil || factory == nil {
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
			_ = listenAria2Events(ctx, wsURL, notifier, factory)
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

func listenAria2Events(ctx context.Context, wsURL string, notifier *botNotifier, factory aria2ControllerFactory) error {
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

		go handleAria2Event(ctx, notifier, factory, event.Method, gid)
	}
}

func handleAria2Event(ctx context.Context, notifier *botNotifier, factory aria2ControllerFactory, method, gid string) {
	if method != aria2EventDownloadComplete {
		return
	}
	if !aria2DownloaderEnabled() {
		return
	}

	cmdCtx, cancel := context.WithTimeout(ctx, aria2CommandTimeout)
	defer cancel()

	controller := factory()
	task, err := controller.TellStatus(cmdCtx, gid)
	if err != nil {
		return
	}

	notifyAria2DownloadComplete(ctx, notifier, task)
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
