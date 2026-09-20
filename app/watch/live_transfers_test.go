package watch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	apparia2 "github.com/snakexgc/tdl/app/aria2"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/application"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
	pkgtclient "github.com/snakexgc/tdl/pkg/tclient"
)

const liveFixtureName = "tdl-serial-fixture-4MiB.bin"

// This receipt contains only IDs of test-created groups, never credentials.
// It is deliberately retained so a failed/repeated test reuses its groups.
type liveFixtureReceipt struct {
	Groups    []int64 `json:"groups"`
	MessageID int     `json:"message_id"`
}

func liveTransfers(t *testing.T, ctx context.Context, root string, cfg *config.Config, store *storage.Memory, client *pkgtclient.Client) error {
	t.Helper()
	directory := filepath.Join(root, ".tdl", "live-validation")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	receiptPath := filepath.Join(directory, "fixtures.json")
	var receipt liveFixtureReceipt
	if data, err := os.ReadFile(receiptPath); err == nil {
		require.NoError(t, json.Unmarshal(data, &receipt))
	} else {
		require.True(t, os.IsNotExist(err))
	}
	saveReceipt := func() {
		data, err := json.MarshalIndent(receipt, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(receiptPath, data, 0o600))
	}
	peerStore, err := tgauth.PeersStore(store)
	require.NoError(t, err)
	manager := peers.Options{Storage: peerStore}.Build(client.API())
	dialogs, err := tgauth.Dialogs(ctx, client.API(), store)
	require.NoError(t, err)
	t.Logf("production dialog catalog read: %d accessible chats (content/names not logged)", len(dialogs))
	for len(receipt.Groups) < 3 {
		titles := []string{"TDL 架构测试 20260919 来源", "TDL 架构测试 20260919 目标A", "TDL 架构测试 20260919 目标B"}
		updates, err := client.API().ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
			Megagroup: true, Title: titles[len(receipt.Groups)], About: "用户授权的 TDL 串行功能测试群；不邀请成员，测试后保留。",
		})
		require.NoError(t, err)
		data, ok := updates.(*tg.Updates)
		require.True(t, ok)
		require.NoError(t, manager.Apply(ctx, data.Users, data.Chats))
		var id int64
		for _, chat := range data.Chats {
			if channel, ok := chat.(*tg.Channel); ok && channel.Creator && channel.Title == titles[len(receipt.Groups)] {
				id = channel.ID
			}
		}
		require.NotZero(t, id)
		receipt.Groups = append(receipt.Groups, id)
		saveReceipt()
		t.Logf("created and retained private test group: %s", titles[len(receipt.Groups)-1])
		if err := liveDelay(ctx, 5*time.Second); err != nil {
			return err
		}
	}
	groups := make([]peers.Channel, 0, 3)
	for _, id := range receipt.Groups {
		channel, err := manager.ResolveChannelID(ctx, id)
		require.NoError(t, err)
		require.True(t, channel.Raw().Creator)
		require.True(t, strings.HasPrefix(channel.VisibleName(), "TDL 架构测试 20260919 "), "only test-created groups may be used")
		groups = append(groups, channel)
	}
	payload := make([]byte, 4<<20)
	_, err = rand.New(rand.NewSource(20260919)).Read(payload)
	require.NoError(t, err)
	if receipt.MessageID == 0 {
		file, err := uploader.NewUploader(client.API()).WithThreads(1).FromBytes(ctx, liveFixtureName, payload)
		require.NoError(t, err)
		updates, err := client.API().MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Silent: true, Peer: groups[0].InputPeer(), RandomID: time.Now().UnixNano(), Message: "TDL serial validation fixture v1 (4 MiB)",
			Media: &tg.InputMediaUploadedDocument{
				File: file, MimeType: "application/octet-stream", ForceFile: true,
				Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: liveFixtureName}},
			},
		})
		require.NoError(t, err)
		data, ok := updates.(*tg.Updates)
		require.True(t, ok)
		for _, update := range data.Updates {
			if message, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if value, ok := message.Message.(*tg.Message); ok {
					receipt.MessageID = value.ID
				}
			}
		}
		require.NotZero(t, receipt.MessageID)
		saveReceipt()
		t.Log("uploaded one 4 MiB generated fixture to the private source group")
	}
	message, err := tutil.GetSingleMessage(ctx, client.API(), groups[0].InputPeer(), receipt.MessageID)
	require.NoError(t, err)
	if os.Getenv("TDL_LIVE_STAGE") == "forward" {
		return liveForwardRules(t, ctx, cfg, store, client, manager, groups)
	}
	media, ok := tmedia.GetMedia(message)
	require.True(t, ok)
	require.EqualValues(t, len(payload), media.Size)
	require.Equal(t, client.Config().ThisDC, media.DC, "test refuses extra DC connections")
	pool := liveSinglePool{client.API()}
	cfg.HTTP = config.HTTPConfig{Address: "127.0.0.1", Port: 23950, PublicBaseURL: "http://127.0.0.1:23950", DownloadLinkTTLHours: 24}
	service := httpdl.NewService(cfg, store, zap.NewNop())
	service.Pools().Set(pool)
	task := &httpdl.Task{
		ID: "live-serial-fixture", PeerID: groups[0].ID(), Peer: groups[0].InputPeer(), MessageID: message.ID,
		FileName: liveFixtureName, FileSize: media.Size, Media: media, CreatedAt: time.Now(),
	}
	require.NoError(t, service.Proxy().Tasks().Add(ctx, task))
	controller := httpdl.NewController(ctx, service)
	require.True(t, controller.Start())
	defer controller.Stop()
	require.NoError(t, liveDelay(ctx, 300*time.Millisecond))
	downloadURL, err := service.Proxy().BuildURL(task.ID)
	require.NoError(t, err)
	if os.Getenv("TDL_LIVE_STAGE") != liveHTTPHotStage {
		started := time.Now()
		body := liveHTTP(t, ctx, downloadURL, "", http.StatusOK)
		require.Equal(t, sha256.Sum256(payload), sha256.Sum256(body))
		t.Logf("HTTP: %d bytes verified by SHA-256 in %s (deliberately rate limited)", len(body), time.Since(started).Round(time.Millisecond))
	}
	cfg.HTTP.PublicBaseURL, cfg.HTTP.DownloadLinkTTLHours = "http://localhost:23950", 48
	require.False(t, service.UpdateConfig(cfg), "link settings must not restart the HTTP listener")
	downloadURL, err = service.Proxy().BuildURL(task.ID)
	require.NoError(t, err)
	require.Contains(t, downloadURL, "http://localhost:23950/")
	body := liveHTTP(t, ctx, downloadURL, "bytes=137-65535", http.StatusPartialContent)
	require.Equal(t, payload[137:65536], body)
	t.Log("HTTP link settings hot-applied; non-aligned Range exact bytes verified")
	if os.Getenv("TDL_LIVE_STAGE") == liveHTTPHotStage {
		return nil
	}
	worker := local.New(localSource{proxy: service.Proxy(), scheduler: service.Proxy().Scheduler()}, taskhub.NewLocalRepository(store), zap.NewNop())
	host, executor, err := application.LocalDownloadHost(ctx, types.AccountID(cfg.Namespace), worker)
	require.NoError(t, err)
	defer host.Stop(context.Background())
	opts := Options{Account: types.AccountID(cfg.Namespace), Download: true, Template: "F", FilenameMaxLength: 255, Limit: 1, PoolSize: 1}
	policies, filter, naming, err := startPolicies(ctx, cfg.Namespace, opts)
	require.NoError(t, err)
	defer policies.Stop(context.Background())
	pipelineHost, control, err := application.DownloadControlHost(ctx, opts.Account, nil)
	require.NoError(t, err)
	defer pipelineHost.Stop(context.Background())
	opts.Filter, opts.Naming, opts.DownloadPipeline = filter, naming, control.(ports.DownloadPipeline)
	localProbe := &liveSubmissionProbe{executor: executor}
	route := &fixedDownloadRoute{route: ports.DownloadRoute{Executors: []string{localExecutorName}, LocalRoot: t.TempDir()}}
	opts.DownloadRouting = route
	w := &Watcher{opts: opts, manager: manager, pool: pool, runtime: &watchRuntime{proxy: service.Proxy(), worker: worker, local: localProbe, pools: service.Pools()}}
	pipelineSource := config.NewSource(cfg)
	pipelineCtx := config.WithSource(ctx, pipelineSource)
	intentHost, intents, _, err := application.IntentHost(pipelineCtx, opts.Account, func(context.Context, types.DownloadIntent) error { return nil }, nil, w.processDownloadIntent)
	require.NoError(t, err)
	defer intentHost.Stop(context.Background())
	request := types.DownloadIntent{Account: opts.Account, Peer: plainPeer(groups[0].InputPeer()), PeerID: groups[0].ID(), MessageID: message.ID, Source: downloadJobSourceMessageLink}
	summary, err := intents.(ports.DownloadRequests).Submit(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Queued)
	require.Zero(t, summary.Uncertain)
	require.NotEmpty(t, localProbe.request.TaskID)
	localPath := localProbe.request.FullPath
	require.Equal(t, filepath.Join(route.route.LocalRoot, liveFixtureName), localPath)
	require.NoError(t, liveWait(ctx, func() (bool, error) {
		record, found, err := taskhub.NewLocalRepository(store).Get(ctx, localProbe.request.TaskID)
		if err != nil {
			return false, err
		}
		if record.Status == types.LocalDownloadStatusError {
			return false, fmt.Errorf("local download: %s", record.Error)
		}
		return found && record.Status == types.LocalDownloadStatusComplete, nil
	}))
	localData, err := os.ReadFile(localPath)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(payload), sha256.Sum256(localData))
	t.Log("production download intent -> pipeline -> RTE local downloader: 4 MiB SHA-256 matched")
	ariaCfg := config.Aria2Config{RPCURL: "http://127.0.0.1:23969/jsonrpc", Secret: "tdl-live-validation", TimeoutSeconds: 30}
	rpc := apparia2.NewClient(ariaCfg)
	require.NoError(t, rpc.SetMaxConcurrentDownloads(ctx, 1))
	ariaController := apparia2.NewController(&config.Config{Namespace: cfg.Namespace, Aria2: ariaCfg, HTTP: cfg.HTTP, Limit: 1, PoolSize: 1}, store, zap.NewNop())
	ariaProbe := &liveSubmissionProbe{executor: ariaController}
	w.opts.DownloadSubmitter = ariaProbe
	route.route = ports.DownloadRoute{Executors: []string{config.DownloadExecutorAria2}}
	ariaRoot := t.TempDir()
	pipelineConfig, err := config.Clone(cfg)
	require.NoError(t, err)
	pipelineConfig.Aria2.Dir = ariaRoot
	pipelineSource.Replace(pipelineConfig)
	summary, err = intents.(ports.DownloadRequests).Submit(pipelineCtx, request)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Queued)
	require.Zero(t, summary.Uncertain)
	require.NotEmpty(t, ariaProbe.result.ID)
	ariaPath := ariaProbe.request.FullPath
	require.Equal(t, filepath.Join(ariaRoot, liveFixtureName), ariaPath)
	require.NoError(t, liveWait(ctx, func() (bool, error) {
		status, err := rpc.TellStatus(ctx, ariaProbe.result.ID)
		if err != nil {
			return false, err
		}
		if status.Status == "error" {
			return false, fmt.Errorf("aria2 download: %s", status.ErrorMessage)
		}
		return status.Status == types.LocalDownloadStatusComplete, nil
	}))
	ariaData, err := os.ReadFile(ariaPath)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(payload), sha256.Sum256(ariaData))
	t.Log("production download intent -> pipeline -> real aria2 -> HTTP -> Telegram: 4 MiB SHA-256 matched")
	return nil
}

// The test remains serial; captures are read only after the component's
// synchronous admission result, before the next executor is bound.
type liveSubmissionProbe struct {
	executor ports.DownloadExecutor
	request  types.DownloadSubmission
	result   types.DownloadResult
}

func (p *liveSubmissionProbe) Name() string { return p.executor.Name() }
func (p *liveSubmissionProbe) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	p.request = in
	var err error
	p.result, err = p.executor.Submit(ctx, in)
	return p.result, err
}

// The uploaded fixture is on the account's current DC; reuse its one existing
// connection rather than creating more transports or takeout sessions.
type liveSinglePool struct{ api *tg.Client }

func (p liveSinglePool) Client(context.Context, int) *tg.Client  { return p.api }
func (p liveSinglePool) Default(context.Context) *tg.Client      { return p.api }
func (p liveSinglePool) Takeout(context.Context, int) *tg.Client { return p.api }
func (liveSinglePool) Close() error                              { return nil }

func liveDelay(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func liveWait(ctx context.Context, check func() (bool, error)) error {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		if err := liveDelay(bounded, time.Second); err != nil {
			return err
		}
	}
}

func liveHTTP(t *testing.T, ctx context.Context, url, byteRange string, code int) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, code, response.StatusCode)
	data, err := io.ReadAll(io.LimitReader(response.Body, 5<<20))
	require.NoError(t, err)
	return data
}
