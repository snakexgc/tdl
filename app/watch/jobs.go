package watch

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/bsw/services/localfs"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
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
	localExecutorName            = "local"
)

type fileTask struct {
	msg        *tg.Message
	triggerMsg *tg.Message
	media      *tmedia.Media
	peer       tg.InputPeerClass
	peerID     int64
}

func (w *Watcher) dispatcher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case submission := <-w.messageLinks:
			call, cancel := context.WithCancel(ctx)
			unlink := func() bool { return false }
			if submission.ctx != nil {
				unlink = context.AfterFunc(submission.ctx, cancel)
				if submission.ctx.Err() != nil {
					cancel()
				}
			}
			result, err := w.submitMessageLink(call, submission.link)
			unlink()
			cancel()
			submission.reply <- messageLinkSubmissionResponse{result: result, err: err}
		}
	}
}

func (w *Watcher) submitMessageLink(ctx context.Context, link string) (MessageLinkSubmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return MessageLinkSubmissionResult{Link: link}, err
	}
	w.intentMu.RLock()
	intents := w.intents
	w.intentMu.RUnlock()
	requests, ok := intents.(ports.DownloadRequests)
	if !ok {
		return MessageLinkSubmissionResult{Link: link}, fmt.Errorf("download requests are unavailable")
	}
	if w.opts.MessageLinks != nil {
		return w.opts.MessageLinks.Submit(ctx, w.reactionAccount(), link, watchMessageSource{watcher: w}, requests)
	}
	link, err := validateMessageLink(ctx, w.opts, link)
	if err != nil {
		return MessageLinkSubmissionResult{Link: link}, err
	}
	request, err := (watchMessageSource{watcher: w}).Resolve(ctx, w.reactionAccount(), link)
	if err != nil {
		return MessageLinkSubmissionResult{Link: link}, err
	}
	request.Link, request.Source = link, downloadJobSourceMessageLink
	return requests.Submit(ctx, request)
}

type watchMessageSource struct{ watcher *Watcher }

func (s watchMessageSource) Resolve(ctx context.Context, account types.AccountID, link string) (types.DownloadIntent, error) {
	if account != s.watcher.reactionAccount() {
		return types.DownloadIntent{}, fmt.Errorf("message source account mismatch")
	}
	peer, messageID, err := tutil.ParseMessageLink(ctx, s.watcher.manager, link)
	if err != nil {
		return types.DownloadIntent{}, err
	}
	return types.DownloadIntent{Account: account, Peer: plainPeer(peer.InputPeer()), PeerID: peer.ID(), MessageID: messageID}, nil
}

// The adapter binds this connection's media source and live account ports.
// All preparation, filtering, routing and admission decisions belong to SWCs.
func (w *Watcher) processDownloadIntent(ctx context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	if request.Account != w.reactionAccount() || !w.downloadEnabled() {
		return types.DownloadSubmissionSummary{}, fmt.Errorf("download source account is unavailable")
	}
	if w.opts.DownloadPipeline == nil {
		return types.DownloadSubmissionSummary{}, fmt.Errorf("download pipeline is unavailable")
	}
	cfg := config.From(ctx)
	result, err := w.opts.DownloadPipeline.SubmitBatch(ctx, request, ports.DownloadResources{
		Source: watchDownloadSource{watcher: w}, Filter: w.opts.Filter, Naming: w.opts.Naming,
		Routing: w.opts.DownloadRouting, Files: localfs.Downloads{},
		Executors: map[string]ports.DownloadExecutor{localExecutorName: w.runtime.local, config.DownloadExecutorAria2: w.opts.DownloadSubmitter},
		Defaults: ports.DownloadDefaults{
			RemoteRoot: cfg.Aria2.Dir, SkipSame: w.opts.SkipSame, Limit: effectiveWatchOptionLimit(w.opts.Limit, cfg),
		},
	})
	for _, link := range result.Links {
		w.notify(ctx, "已生成临时 HTTP 下载链接。\n文件：%s\n链接：%s", link.FileName, link.URL)
	}
	if err != nil {
		w.notify(ctx, "下载提交未全部完成。\n链接：%s\n已接受：%d\n跳过：%d\n未提交：%d\n结果待确认：%d\n错误：%v", request.Link, result.Queued, result.Skipped, result.Failed, result.Uncertain, err)
	} else {
		w.notify(ctx, "下载提交完成。\n链接：%s\n文件总数：%d\n已接受：%d\n跳过：%d", request.Link, result.Total, result.Queued, result.Skipped)
	}
	return result, err
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

func (w *Watcher) notify(ctx context.Context, format string, args ...interface{}) {
	if w.opts.Notify != nil {
		w.opts.Notify(ctx, fmt.Sprintf(format, args...))
	}
}
