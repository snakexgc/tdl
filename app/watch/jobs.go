package watch

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	watcharia2 "github.com/iyear/tdl/app/watch/aria2"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
)

type downloadJob struct {
	peer   tg.InputPeerClass
	msgID  int
	peerID int64
	link   string
	source string
}

const (
	downloadJobSourceReaction    = "reaction"
	downloadJobSourceMessageLink = "message_link"
)

type fileTask struct {
	msg        *tg.Message
	triggerMsg *tg.Message
	media      *tmedia.Media
	peer       tg.InputPeerClass
	peerID     int64
}

type fileCollection struct {
	files   []fileTask
	total   int
	skipped int
}

type preparedFileTask struct {
	file     fileTask
	fileName string
	dir      string
	out      string
	fullPath string
}

func (w *Watcher) dispatcher(ctx context.Context, eg *errgroup.Group) {
	for {
		select {
		case <-ctx.Done():
			return
		case submission := <-w.messageLinks:
			result, err := w.submitMessageLink(ctx, eg, submission.link)
			submission.reply <- messageLinkSubmissionResponse{result: result, err: err}
		case job := <-w.jobCh:
			if ctx.Err() != nil {
				return
			}
			_, _ = w.processDownloadJob(ctx, eg, job)
		}
	}
}

func (w *Watcher) submitMessageLink(ctx context.Context, eg *errgroup.Group, link string) (MessageLinkSubmissionResult, error) {
	link, err := ValidateTelegramMessageHTTPLink(link)
	if err != nil {
		return MessageLinkSubmissionResult{Link: link}, err
	}

	peer, msgID, err := tutil.ParseMessageLink(ctx, w.manager, link)
	if err != nil {
		return MessageLinkSubmissionResult{Link: link}, errors.Wrap(err, "解析 Telegram 消息链接")
	}

	job := downloadJob{
		peer:   peer.InputPeer(),
		msgID:  msgID,
		peerID: peer.ID(),
		link:   link,
		source: downloadJobSourceMessageLink,
	}
	return w.processDownloadJob(ctx, eg, job)
}

func (w *Watcher) processDownloadJob(ctx context.Context, eg *errgroup.Group, job downloadJob) (MessageLinkSubmissionResult, error) {
	result := MessageLinkSubmissionResult{
		Link:      job.link,
		PeerID:    job.peerID,
		MessageID: job.msgID,
	}
	logctx.From(ctx).Info("Dispatcher processing job",
		zap.Int64("peer_id", job.peerID),
		zap.Int("msg_id", job.msgID),
		zap.Bool("peer_nil", job.peer == nil),
		zap.String("source", job.source))

	peer := job.peer
	if peer == nil {
		resolved, err := w.resolvePeer(ctx, job.peerID)
		if err != nil {
			logctx.From(ctx).Error("Failed to resolve peer",
				zap.Int64("peer_id", job.peerID),
				zap.Error(err))
			color.Red("❌ Cannot resolve peer %d: %v", job.peerID, err)
			w.notify(ctx, "无法解析消息来源，下载任务未提交。\n消息：%s\nPeer: %d\n错误：%v", job.link, job.peerID, err)
			return result, err
		}
		peer = resolved
	}

	msg, err := tutil.GetSingleMessage(ctx, w.pool.Default(ctx), peer, job.msgID)
	if err != nil {
		logctx.From(ctx).Error("Failed to get message",
			zap.Int("msg_id", job.msgID),
			zap.Error(err))
		color.Red("❌ Cannot get message %d: %v", job.msgID, err)
		w.notify(ctx, "无法获取触发消息，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, job.msgID, err)
		return result, err
	}

	collection, err := w.collectFiles(ctx, msg, peer, job.peerID)
	if err != nil {
		color.Red("❌ Failed to collect files for msg %d: %v", job.msgID, err)
		w.notify(ctx, "解析消息媒体失败，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, job.msgID, err)
		return result, err
	}
	result.Total = collection.total

	prepared := make([]preparedFileTask, 0, len(collection.files))
	for _, f := range collection.files {
		task, skip, err := w.prepareSingle(ctx, f)
		if err != nil {
			color.Red("❌ Failed to prepare file for msg %d: %v", f.msg.ID, err)
			w.notify(ctx, "准备下载任务失败，下载任务未提交。\n消息：%s\n消息 ID: %d\n错误：%v", job.link, f.msg.ID, err)
			continue
		}
		if skip {
			collection.skipped++
			continue
		}
		prepared = append(prepared, task)
	}
	result.Queued = len(prepared)
	result.Skipped = collection.skipped

	w.notify(ctx, "%s\n链接：%s\n文件总数：%d\n需要下载：%d\n跳过：%d", downloadJobNotice(job), job.link, collection.total, len(prepared), collection.skipped)
	if len(prepared) == 0 {
		return result, nil
	}

	for _, f := range prepared {
		f := f
		eg.Go(func() error {
			if err := w.submitSingle(ctx, f); err != nil {
				if !errors.Is(err, context.Canceled) {
					logctx.From(ctx).Error("Submission failed",
						zap.Int("msg_id", f.file.msg.ID),
						zap.String("name", f.file.media.Name),
						zap.Error(err))
					color.Red("❌ Submission failed: msg %d (%s): %v", f.file.msg.ID, f.file.media.Name, err)
					target := "aria2"
					if config.EffectiveDownloaderMode(config.Get()) == config.DownloaderModeInternal {
						target = "内部下载队列"
					}
					w.notify(ctx, "提交到%s失败。\n文件：%s\n消息 ID: %d\n错误：%v", target, f.file.media.Name, f.file.msg.ID, err)
				}
			}
			return nil
		})
	}

	return result, nil
}

func downloadJobNotice(job downloadJob) string {
	if job.source == downloadJobSourceMessageLink {
		return "收到 Telegram 消息链接，已进入下载流程。"
	}
	return "监听到了新增回应触发。"
}

func (w *Watcher) collectFiles(ctx context.Context, msg *tg.Message, peer tg.InputPeerClass, peerID int64) (fileCollection, error) {
	var collection fileCollection

	if groupedID, ok := msg.GetGroupedID(); ok {
		logctx.From(ctx).Info("Grouped message detected",
			zap.Int("msg_id", msg.ID),
			zap.Int64("grouped_id", groupedID))

		from, err := w.manager.FromInputPeer(ctx, peer)
		if err != nil {
			return fileCollection{}, errors.Wrap(err, "resolve input peer")
		}

		grouped, err := tutil.GetGroupedMessages(ctx, w.pool.Default(ctx), from.InputPeer(), msg)
		if err != nil {
			return fileCollection{}, errors.Wrap(err, "get grouped messages")
		}

		color.Cyan("📁 Album detected: %d items, queueing all...", len(grouped))
		for _, m := range grouped {
			media, ok := tmedia.GetMedia(m)
			if !ok {
				continue
			}
			collection.total++
			if !w.matchFilter(media.Name, media.Size) {
				color.Yellow("⏭ Skipping filtered (album): %s", media.Name)
				collection.skipped++
				continue
			}
			collection.files = append(collection.files, fileTask{msg: m, triggerMsg: msg, media: media, peer: peer, peerID: peerID})
		}

		return collection, nil
	}

	media, ok := tmedia.GetMedia(msg)
	if !ok {
		color.Yellow("⚠️ Message %d has no media, skipping", msg.ID)
		return collection, nil
	}
	collection.total++
	if !w.matchFilter(media.Name, media.Size) {
		color.Yellow("⏭ Skipping filtered: %s", media.Name)
		collection.skipped++
		return collection, nil
	}

	collection.files = append(collection.files, fileTask{msg: msg, triggerMsg: msg, media: media, peer: peer, peerID: peerID})
	return collection, nil
}

func (w *Watcher) resolvePeer(ctx context.Context, peerID int64) (tg.InputPeerClass, error) {
	if p, err := w.manager.ResolveChannelID(ctx, peerID); err == nil {
		return p.InputPeer(), nil
	}
	if p, err := w.manager.ResolveUserID(ctx, peerID); err == nil {
		return p.InputPeer(), nil
	}
	if p, err := w.manager.ResolveChatID(ctx, peerID); err == nil {
		return p.InputPeer(), nil
	}
	return nil, fmt.Errorf("cannot resolve peer %d via manager", peerID)
}

func (w *Watcher) prepareSingle(ctx context.Context, file fileTask) (preparedFileTask, bool, error) {
	dialogID := tutil.GetInputPeerID(file.peer)
	data := w.downloadDirData(ctx, file)
	fileName, err := w.renderFileName(dialogID, data.Name, data.Time, file.msg, file.triggerMsg, file.media)
	if err != nil {
		return preparedFileTask{}, false, err
	}

	baseDir := joinTargetPath(w.runtime.outputRoot, renderDownloadDir(w.opts.Dir, data)...)
	dir, out, fullPath := resolveTargetPath(baseDir, fileName)
	if w.runtime.ensureOutputDirs && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return preparedFileTask{}, false, errors.Wrap(err, "create target directory")
		}
	}
	if w.opts.SkipSame && dir != "" {
		if stat, statErr := os.Stat(fullPath); statErr == nil && stat.Size() == file.media.Size {
			color.Yellow("⏭ Skipping existing: %s", fullPath)
			return preparedFileTask{}, true, nil
		}
	}

	return preparedFileTask{
		file:     file,
		fileName: fileName,
		dir:      dir,
		out:      out,
		fullPath: fullPath,
	}, false, nil
}

func (w *Watcher) submitSingle(ctx context.Context, prepared preparedFileTask) error {
	cfg := config.Get()
	file := prepared.file
	task, err := w.runtime.proxy.NewTask(ctx, file.peerID, file.msg.ID, file.peer, prepared.fileName, file.media.Size, file.media)
	if err != nil {
		return errors.Wrap(err, "register download task")
	}

	if config.EffectiveDownloaderMode(cfg) == config.DownloaderModeInternal {
		if _, err := w.runtime.internal.Add(ctx, task, prepared); err != nil {
			return errors.Wrap(err, "queue internal download")
		}
		logctx.From(ctx).Info("Queued internal download task",
			zap.Int64("peer_id", file.peerID),
			zap.Int("msg_id", file.msg.ID),
			zap.String("file_name", prepared.fileName),
			zap.String("target_path", prepared.fullPath),
			zap.String("task_id", task.ID))

		color.Green("🚀 Queued internal download: msg %d -> %s", file.msg.ID, prepared.fullPath)
		color.Green("   Task: %s", task.ID)
		return nil
	}

	downloadURL, err := w.runtime.proxy.BuildURL(task.ID)
	if err != nil {
		return errors.Wrap(err, "build download url")
	}

	connections := config.HTTPRangeConnectionsFor(cfg.HTTP, w.opts.Threads)
	gid, err := w.runtime.aria2.AddURI(ctx, downloadURL, watcharia2.AddURIOptions{
		Dir:         prepared.dir,
		Out:         prepared.out,
		Connections: connections,
	})
	if err != nil {
		return errors.Wrap(err, "submit to aria2")
	}
	if err := w.runtime.aria2Tasks.Add(ctx, watcharia2.TaskRecord{
		GID:          gid,
		TaskID:       task.ID,
		DownloadURL:  downloadURL,
		Dir:          prepared.dir,
		Out:          prepared.out,
		Connections:  connections,
		TransferMode: config.EffectiveHTTPTransferMode(cfg),
		CreatedAt:    time.Now(),
	}); err != nil {
		logctx.From(ctx).Warn("Failed to register aria2 task",
			zap.String("gid", gid),
			zap.String("task_id", task.ID),
			zap.String("download_url", downloadURL),
			zap.Error(err))
	}

	logctx.From(ctx).Info("Submitted aria2 task",
		zap.Int64("peer_id", file.peerID),
		zap.Int("msg_id", file.msg.ID),
		zap.String("file_name", prepared.fileName),
		zap.String("target_path", prepared.fullPath),
		zap.String("download_url", downloadURL),
		zap.String("gid", gid))

	color.Green("🚀 Submitted to aria2: msg %d -> %s", file.msg.ID, prepared.fullPath)
	color.Green("   URL: %s", downloadURL)
	color.Green("   GID: %s", gid)

	return nil
}

func (w *Watcher) notify(ctx context.Context, format string, args ...interface{}) {
	if w.opts.Notify == nil {
		return
	}

	text := fmt.Sprintf(format, args...)
	go w.opts.Notify(context.WithoutCancel(ctx), text)
}
