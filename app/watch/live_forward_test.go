package watch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	appforward "github.com/snakexgc/tdl/app/forward"
	"github.com/snakexgc/tdl/application"
	forwardrules "github.com/snakexgc/tdl/application/forward.rules"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	pkgtclient "github.com/snakexgc/tdl/pkg/tclient"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/configtest"
)

func liveForwardRules(t *testing.T, ctx context.Context, cfg *config.Config, store *storage.Memory, client *pkgtclient.Client, manager *peers.Manager, groups []peers.Channel) error {
	t.Helper()
	ref := func(group peers.Channel) types.ChatRef { return types.ChatRef(fmt.Sprintf("channel:%d", group.ID())) }
	rules := []types.ForwardRule{
		{ID: "live-fanout", Name: "Serial live fanout", Enabled: true, Sources: []types.ChatRef{ref(groups[0])}, Targets: []types.ChatRef{ref(groups[1]), ref(groups[2])}, Mode: testForwardMode, Silent: true},
		{ID: "live-overlap", Name: "Overlapping target", Enabled: true, Sources: []types.ChatRef{ref(groups[0])}, Targets: []types.ChatRef{ref(groups[1])}, Mode: "clone", Silent: true},
	}
	registry := rte.NewRegistry()
	require.NoError(t, forwardrules.Register(registry))
	host, err := registry.Build(types.AccountID(cfg.Namespace), nil, map[string]map[string]any{forwardrules.ID: {"rules": rules}})
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	defer host.Stop(context.Background())
	port, err := host.Resolve(ports.ForwardRulesName)
	require.NoError(t, err)
	policy := port.(ports.ForwardRules)
	destinations := policy.Destinations(ctx, ref(groups[0]))
	require.Len(t, destinations, 2)
	componentStore := configtest.NewStore()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "forwarder", map[string]any{"max_attempts": 1})
	require.NoError(t, err)
	require.NoError(t, componentStore.Save(ctx, "forwarder", true, view))
	queue := appforward.NewQueue(store)
	queueCtx, cancel := context.WithCancel(ctx)
	ready, finished := make(chan struct{}), make(chan struct{})
	var serveErr error
	go func() {
		defer close(finished)
		serveErr = queue.Serve(queueCtx, appforward.Runtime{Account: types.AccountID(cfg.Namespace), Pool: liveSinglePool{client.API()}, Manager: manager, PoolSize: 1, ComponentStore: componentStore, Rules: policy, OnReady: func() { close(ready) }})
	}()
	defer func() { cancel(); <-finished }()
	select {
	case <-ready:
	case <-finished:
		return fmt.Errorf("forward component failed before binding: %w", serveErr)
	case <-ctx.Done():
		return ctx.Err()
	}
	text := fmt.Sprintf("TDL serial fanout verification %d", time.Now().Unix())
	messageID := liveSendText(t, ctx, client.API(), groups[0].InputPeer(), text)
	message := types.ForwardMessage{Account: types.AccountID(cfg.Namespace), Peer: plainPeer(groups[0].InputPeer()), MessageID: messageID, Origin: "TDL private source", Automatic: true}
	require.NoError(t, queue.SubmitMessage(ctx, message))
	accepted, err := queue.List(ctx)
	require.NoError(t, err)
	require.Len(t, accepted, 2)
	for _, destination := range destinations {
		id := liveForwardJobID(t, accepted, messageID, destination.Target)
		require.NoError(t, liveWait(ctx, func() (bool, error) {
			jobs, err := queue.List(ctx)
			if err != nil {
				return false, err
			}
			for _, job := range jobs {
				if job.ID != id {
					continue
				}
				if job.Status == types.StatusError {
					return false, fmt.Errorf("forward failed: %s", job.Error)
				}
				return job.Status == types.StatusDone, nil
			}
			return false, nil
		}))
	}
	require.NoError(t, queue.SubmitMessage(ctx, message))
	replayed, err := queue.List(ctx)
	require.NoError(t, err)
	require.Len(t, replayed, len(accepted))
	for _, destination := range destinations {
		require.Equal(t, liveForwardJobID(t, accepted, messageID, destination.Target), liveForwardJobID(t, replayed, messageID, destination.Target))
	}
	for _, group := range groups[1:] {
		count, official := liveCountText(t, ctx, client.API(), group.InputPeer(), text)
		require.Equal(t, 1, count)
		require.True(t, official)
	}
	t.Log("two matching rules -> two private target groups; exactly one official forward per target; replay deduplicated")
	rules[0].Enabled = false
	require.NoError(t, host.Reconfigure(ctx, forwardrules.ID, map[string]any{"rules": rules}))
	destinations = policy.Destinations(ctx, ref(groups[0]))
	require.Len(t, destinations, 1)
	require.Equal(t, "clone", destinations[0].Mode)
	cloneText := fmt.Sprintf("TDL serial hot-rule clone verification %d", time.Now().Unix())
	cloneID := liveSendText(t, ctx, client.API(), groups[0].InputPeer(), cloneText)
	message.MessageID = cloneID
	require.NoError(t, queue.SubmitMessage(ctx, message))
	accepted, err = queue.List(ctx)
	require.NoError(t, err)
	id := liveForwardJobID(t, accepted, cloneID, destinations[0].Target)
	require.NoError(t, liveWait(ctx, func() (bool, error) {
		jobs, err := queue.List(ctx)
		if err != nil {
			return false, err
		}
		for _, job := range jobs {
			if job.ID == id {
				if job.Status == types.StatusError {
					return false, fmt.Errorf("clone failed: %s", job.Error)
				}
				return job.Status == types.StatusDone, nil
			}
		}
		return false, nil
	}))
	count, official := liveCountText(t, ctx, client.API(), groups[1].InputPeer(), cloneText)
	require.Equal(t, 1, count)
	require.False(t, official)
	count, _ = liveCountText(t, ctx, client.API(), groups[2].InputPeer(), cloneText)
	require.Zero(t, count)
	t.Log("hot rule update: only target A received one clone; disabled fanout sent nothing to target B")
	return nil
}

func liveForwardJobID(t *testing.T, jobs []types.ForwardJob, messageID int, target types.ChatRef) string {
	t.Helper()
	for _, job := range jobs {
		if job.SourceMessageID == messageID && job.Destination == string(target) {
			return job.ID
		}
	}
	t.Fatal("routed forward was not persisted")
	return ""
}

func liveSendText(t *testing.T, ctx context.Context, api *tg.Client, peer tg.InputPeerClass, text string) int {
	t.Helper()
	updates, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: text, RandomID: time.Now().UnixNano(), Silent: true})
	require.NoError(t, err)
	if short, ok := updates.(*tg.UpdateShortSentMessage); ok {
		return short.ID
	}
	value, ok := updates.(interface{ GetUpdates() []tg.UpdateClass })
	require.True(t, ok)
	for _, update := range value.GetUpdates() {
		if message, ok := update.(*tg.UpdateNewChannelMessage); ok {
			if message, ok := message.Message.(*tg.Message); ok {
				return message.ID
			}
		}
	}
	t.Fatal("sent test message ID missing")
	return 0
}

func liveCountText(t *testing.T, ctx context.Context, api *tg.Client, peer tg.InputPeerClass, text string) (int, bool) {
	t.Helper()
	history, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 10})
	require.NoError(t, err)
	value, ok := history.(interface{ GetMessages() []tg.MessageClass })
	require.True(t, ok)
	count, official := 0, false
	for _, item := range value.GetMessages() {
		if message, ok := item.(*tg.Message); ok && message.Message == text {
			count++
			_, official = message.GetFwdFrom()
		}
	}
	return count, official
}
