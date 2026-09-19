package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/application/forwarder"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const testStoragePath = "path"

func TestIntentToggleRetainsSiblingAndRefreshesExistingFacade(t *testing.T) {
	ctx := context.Background()
	store := config.NewStore(t.TempDir())
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "trigger.download", nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "trigger.download", false, view))
	forwarded := make(chan struct{}, 1)
	host, downloads, forwards, err := application.IntentHostStored(ctx, types.DefaultAccount,
		func(context.Context, types.DownloadIntent) error { return nil },
		func(context.Context, types.ForwardIntent) error { forwarded <- struct{}{}; return nil }, store,
		func(context.Context, types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
			return types.DownloadSubmissionSummary{}, nil
		})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	original, err := host.Resolve(ports.ForwardIntentsName)
	require.NoError(t, err)
	request := types.DownloadIntent{Account: types.DefaultAccount, PeerID: 10, MessageID: 1}
	require.Error(t, downloads.Publish(ctx, request))
	for _, enabled := range []bool{true, false, true} {
		require.NoError(t, store.Save(ctx, "trigger.download", enabled, view))
		require.NoError(t, host.ReconcileSaved(ctx, store))
		current, err := host.Resolve(ports.ForwardIntentsName)
		require.NoError(t, err)
		require.Same(t, original, current)
		_, err = downloads.(ports.DownloadRequests).Submit(ctx, request)
		if enabled {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	require.NoError(t, forwards.Publish(ctx, types.ForwardIntent{Account: types.DefaultAccount, PeerID: 10, MessageID: 1}))
	select {
	case <-forwarded:
	case <-time.After(time.Second):
		t.Fatal("unrelated forward consumer stopped")
	}
	listening := forwards.(ports.ForwardListening)
	require.NoError(t, host.PatchSaved(ctx, "trigger.forward", map[string]any{"listen": []string{"channel:10"}, "listen_comments": false}, store))
	settings, err := listening.Listening(ctx, types.DefaultAccount)
	require.NoError(t, err)
	require.Equal(t, []string{"channel:10"}, settings.Sources)
	view, err = catalog.View(ctx, "trigger.forward", map[string]any{"listen": []string{"chat:20"}})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "trigger.forward", false, view))
	require.NoError(t, host.ReconcileSaved(ctx, store))
	_, err = listening.Listening(ctx, types.DefaultAccount)
	require.Error(t, err)
	require.NoError(t, store.Save(ctx, "trigger.forward", true, view))
	require.NoError(t, host.ReconcileSaved(ctx, store))
	settings, err = listening.Listening(ctx, types.DefaultAccount)
	require.NoError(t, err)
	require.Equal(t, []string{"chat:20"}, settings.Sources, "existing consumers resolve the replacement policy")
}

type completionTransport struct{ completed chan string }

type forwardPeers struct{}

func (forwardPeers) ResolveForwardPeer(context.Context, types.AccountID, string, bool) (types.ForwardPeer, error) {
	return types.ForwardPeer{Reference: "chat:901"}, nil
}

func (p completionTransport) Forward(_ context.Context, job *types.ForwardJob, _ func(types.ForwardJob)) error {
	p.completed <- job.ID
	return nil
}

func TestForwarderCanBeEnabledTwiceWithoutReplacingConnectionHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "tasks")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	storage, err := engine.Open(string(types.DefaultAccount))
	require.NoError(t, err)
	queue := application.NewForwardQueue(taskhub.NewForwardRepository(storage))
	store := config.NewStore(t.TempDir())
	catalog, err := application.Catalog()
	require.NoError(t, err)
	view, err := catalog.View(ctx, "forwarder", nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "forwarder", false, view))
	bound := make(chan *rte.Runtime, 2)
	completed := make(chan string, 2)
	exited := make(chan error, 1)
	go func() {
		exited <- application.ServeForwardQueue(ctx, types.DefaultAccount, queue, completionTransport{completed}, func(host *rte.Runtime) { bound <- host }, application.ForwardOptions{Store: store, Peers: forwardPeers{}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			t.Error("connection host did not drain")
		}
	})
	var host *rte.Runtime
	select {
	case host = <-bound:
	case err := <-exited:
		t.Fatalf("disabled forwarder lost its owner: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("host did not bind")
	}
	directory := rte.NewDirectory(catalog, store)
	require.NoError(t, directory.Bind("forward", func() *rte.Runtime { return host }))
	declaration := forwarder.Commands()[0]
	request := types.ConsoleRequest{Account: types.DefaultAccount, Name: declaration.Name, Text: "/forward", ReplyText: "https://t.me/c/10/5", Private: true}
	var previous ports.ConsoleCommandHandler
	var previousRouter ports.ForwardRouting
	for i := range 2 {
		require.NoError(t, store.Save(ctx, "forwarder", true, view))
		require.NoError(t, host.ReconcileSaved(ctx, store))
		value, err := directory.ResolveComponentPort(forwarder.ID, declaration.Port)
		require.NoError(t, err)
		command := value.(ports.ConsoleCommandHandler)
		value, err = directory.ResolveComponentPort(forwarder.ID, ports.ForwardRoutingName)
		require.NoError(t, err)
		router := value.(ports.ForwardRouting)
		message := types.ForwardMessage{Account: types.DefaultAccount, Peer: types.MessagePeer{Kind: "channel", ID: 10}, MessageID: 100 + i}
		if previous != nil {
			_, err := previous.Execute(ctx, request)
			require.ErrorContains(t, err, "stopped")
			require.ErrorContains(t, previousRouter.SubmitMessage(ctx, message), "stopped")
		}
		id, err := queue.EnqueueMessage(ctx, 10, i+1, "source", "target", "", "default", false)
		require.NoError(t, err)
		select {
		case actual := <-completed:
			require.Equal(t, id, actual)
		case <-time.After(3 * time.Second):
			t.Fatal("re-enabled worker did not execute")
		}
		require.NoError(t, host.PatchSaved(ctx, forwarder.ID, map[string]any{"target": "@command-target", "mode": "clone", "silent": true}, store))
		invalid := request
		invalid.ReplyText = "https://example.com/file.zip"
		response, err := command.Execute(ctx, invalid)
		require.NoError(t, err)
		require.Contains(t, response.Text, "用法")
		response, err = command.Execute(ctx, request)
		require.NoError(t, err)
		require.Contains(t, response.Text, "1 条")
		select {
		case commandID := <-completed:
			jobs, err := queue.List(ctx)
			require.NoError(t, err)
			found := false
			for _, job := range jobs {
				if job.ID == commandID {
					found = true
					require.Equal(t, request.ReplyText, job.SourceLink)
					require.Equal(t, "@command-target", job.Destination)
					require.Equal(t, "clone", job.Mode)
					require.True(t, job.Silent)
				}
			}
			require.True(t, found)
		case <-time.After(3 * time.Second):
			t.Fatal("declared command did not reach the production queue")
		}
		require.NoError(t, router.SubmitMessage(ctx, message))
		select {
		case id := <-completed:
			jobs, err := queue.List(ctx)
			require.NoError(t, err)
			found := false
			for _, job := range jobs {
				if job.ID == id {
					found = true
					require.Equal(t, "channel", job.SourcePeerKind)
					require.Equal(t, message.Peer.ID, job.SourcePeerID)
					require.Equal(t, "chat:901", job.Destination)
					require.Equal(t, "clone", job.Mode)
					require.True(t, job.Silent)
				}
			}
			require.True(t, found)
		case <-time.After(3 * time.Second):
			t.Fatal("message router did not reach the production queue")
		}
		require.NoError(t, store.Save(ctx, "forwarder", false, view))
		require.NoError(t, host.ReconcileSaved(ctx, store))
		_, err = command.Execute(ctx, request)
		require.ErrorContains(t, err, "stopped")
		require.ErrorContains(t, router.SubmitMessage(ctx, message), "stopped")
		_, err = directory.ResolveComponentPort(forwarder.ID, declaration.Port)
		require.Error(t, err)
		previous = command
		previousRouter = router
		select {
		case <-bound:
			t.Fatal("feature stop detached the connection host")
		case err := <-exited:
			t.Fatalf("feature stop terminated host: %v", err)
		default:
		}
	}
}
