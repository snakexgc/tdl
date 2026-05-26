package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/utils"
)

const (
	aria2EventReconnectDelay = 3 * time.Second
	aria2ProgressBarWidth    = 10
)

const (
	aria2EventDownloadStart    = "aria2.onDownloadStart"
	aria2EventDownloadComplete = "aria2.onDownloadComplete"
	aria2EventDownloadPause    = "aria2.onDownloadPause"
	aria2EventDownloadError    = "aria2.onDownloadError"
)

type aria2Event struct {
	Method string            `json:"method"`
	Params []aria2EventParam `json:"params"`
}

type aria2EventParam struct {
	GID string `json:"gid"`
}

// aria2ProgressEntry tracks an in-flight progress-update loop for a single GID.
type aria2ProgressEntry struct {
	cancel  context.CancelFunc
	tracked []trackedMessage
}

// aria2ProgressTracker maps GIDs to their live-progress state.
type aria2ProgressTracker struct {
	mu    sync.Mutex
	items map[string]*aria2ProgressEntry
}

func newAria2ProgressTracker() *aria2ProgressTracker {
	return &aria2ProgressTracker{items: make(map[string]*aria2ProgressEntry)}
}

// Set registers (or replaces) a progress entry for gid, cancelling any previous one.
func (t *aria2ProgressTracker) Set(gid string, cancel context.CancelFunc, tracked []trackedMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.items[gid]; ok {
		prev.cancel()
	}
	t.items[gid] = &aria2ProgressEntry{cancel: cancel, tracked: tracked}
}

// Cancel stops the progress loop for gid and returns the tracked messages (nil if none).
func (t *aria2ProgressTracker) Cancel(gid string) []trackedMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.items[gid]
	if !ok {
		return nil
	}
	entry.cancel()
	delete(t.items, gid)
	return entry.tracked
}

func runAria2EventListener(ctx context.Context, notifier *botNotifier, factory aria2ControllerFactory) {
	if notifier == nil || factory == nil {
		return
	}

	tracker := newAria2ProgressTracker()

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
			_ = listenAria2Events(ctx, wsURL, notifier, factory, tracker)
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

func listenAria2Events(ctx context.Context, wsURL string, notifier *botNotifier, factory aria2ControllerFactory, tracker *aria2ProgressTracker) error {
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

		go handleAria2Event(ctx, notifier, factory, tracker, event.Method, gid)
	}
}

func handleAria2Event(ctx context.Context, notifier *botNotifier, factory aria2ControllerFactory, tracker *aria2ProgressTracker, method, gid string) {
	if !aria2DownloaderEnabled() {
		return
	}

	cfg := config.Get()
	if cfg == nil {
		return
	}

	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aria2CommandTimeout)
	defer cancel()
	controller := factory()

	switch method {
	case aria2EventDownloadStart:
		if !cfg.Bot.Notify.OnDownloadStart {
			return
		}
		task, err := controller.TellStatus(cmdCtx, gid)
		if err != nil {
			return
		}
		text := formatAria2ProgressMessage(task)
		tracked := notifier.SendAndTrack(ctx, text)
		if cfg.Bot.Notify.LiveProgress && len(tracked) > 0 {
			progressCtx, progressCancel := context.WithCancel(context.WithoutCancel(ctx))
			tracker.Set(gid, progressCancel, tracked)
			intervalSec := cfg.Bot.Notify.LiveProgressIntervalSec
			if intervalSec < 5 {
				intervalSec = 15
			}
			go runAria2ProgressLoop(progressCtx, notifier, factory, tracker, gid, tracked, intervalSec)
		}

	case aria2EventDownloadComplete:
		tracked := tracker.Cancel(gid)
		task, err := controller.TellStatus(cmdCtx, gid)
		if err == nil && len(tracked) > 0 {
			notifier.EditTracked(ctx, tracked, formatAria2DownloadFinal(task, "complete"))
		}
		if cfg.Bot.Notify.OnDownloadComplete {
			if err == nil {
				notifyAria2DownloadComplete(ctx, notifier, task)
			} else {
				notifier.Notify(ctx, fmt.Sprintf("下载完成 GID: %s", gid))
			}
		}

	case aria2EventDownloadPause:
		tracked := tracker.Cancel(gid)
		task, err := controller.TellStatus(cmdCtx, gid)
		if cfg.Bot.Notify.OnDownloadPause {
			if err == nil {
				if len(tracked) > 0 {
					notifier.EditTracked(ctx, tracked, formatAria2DownloadFinal(task, "pause"))
				} else {
					notifier.Notify(ctx, fmt.Sprintf("%s 下载已暂停", watch.Aria2TaskName(task)))
				}
			} else {
				notifier.Notify(ctx, fmt.Sprintf("GID %s 下载已暂停", gid))
			}
		}

	case aria2EventDownloadError:
		tracked := tracker.Cancel(gid)
		task, err := controller.TellStatus(cmdCtx, gid)
		if cfg.Bot.Notify.OnDownloadError {
			if err == nil {
				if len(tracked) > 0 {
					notifier.EditTracked(ctx, tracked, formatAria2DownloadFinal(task, "error"))
				} else {
					info := watch.Aria2TaskInfoFromStatus(task)
					msg := watch.Aria2TaskName(task) + " 下载失败"
					if info.ErrorMessage != "" {
						msg += "：" + info.ErrorMessage
					}
					notifier.Notify(ctx, msg)
				}
			} else {
				notifier.Notify(ctx, fmt.Sprintf("GID %s 下载失败", gid))
			}
		}
	}
}

// runAria2ProgressLoop edits the tracked messages every intervalSec seconds until ctx is cancelled.
func runAria2ProgressLoop(
	ctx context.Context,
	notifier *botNotifier,
	factory aria2ControllerFactory,
	tracker *aria2ProgressTracker,
	gid string,
	tracked []trackedMessage,
	intervalSec int,
) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryCtx, cancel := context.WithTimeout(ctx, aria2CommandTimeout)
			task, err := factory().TellStatus(queryCtx, gid)
			cancel()
			if err != nil {
				return
			}
			status := task.Status
			if status == "complete" || status == "error" || status == "removed" {
				tracker.Cancel(gid)
				return
			}
			notifier.EditTracked(ctx, tracked, formatAria2ProgressMessage(task))
		}
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

// formatAria2ProgressMessage formats a live-updating progress message.
func formatAria2ProgressMessage(task watch.Aria2DownloadStatus) string {
	info := watch.Aria2TaskInfoFromStatus(task)
	speed := parseAria2Int(task.DownloadSpeed)
	bar := buildAria2ProgressBar(info.CompletedLength, info.TotalLength, aria2ProgressBarWidth)

	lines := []string{
		"下载中: " + watch.Aria2TaskName(task),
		"进度: " + bar,
		"大小: " + formatAria2Size(info.TotalLength),
		"速度: " + utils.Byte.FormatBinaryBytes(speed) + "/s",
		"更新: " + time.Now().Format("15:04:05"),
	}
	return strings.Join(lines, "\n")
}

// formatAria2DownloadFinal formats the frozen final-state message after a download ends.
func formatAria2DownloadFinal(task watch.Aria2DownloadStatus, reason string) string {
	info := watch.Aria2TaskInfoFromStatus(task)
	bar := buildAria2ProgressBar(info.CompletedLength, info.TotalLength, aria2ProgressBarWidth)

	var statusLine string
	switch reason {
	case "complete":
		statusLine = "✅ 下载完成"
	case "pause":
		statusLine = "⏸ 下载已暂停"
	case "error":
		statusLine = "❌ 下载失败"
		if info.ErrorMessage != "" {
			statusLine += "：" + info.ErrorMessage
		}
	default:
		statusLine = "⏹ 已停止"
	}

	lines := []string{
		statusLine + ": " + watch.Aria2TaskName(task),
		"进度: " + bar,
		"大小: " + formatAria2Size(info.TotalLength),
	}
	return strings.Join(lines, "\n")
}

// buildAria2ProgressBar renders a Unicode block progress bar like [████░░░░░░] 40.00%.
func buildAria2ProgressBar(completed, total int64, width int) string {
	if total <= 0 {
		return fmt.Sprintf("[%s] 0.00%%", strings.Repeat("░", width))
	}
	pct := float64(completed) / float64(total)
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(width))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s] %.2f%%", bar, pct*100)
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
