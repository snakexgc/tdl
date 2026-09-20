package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/ecual/aria2rpc"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
)

// This opt-in test owns its daemon, RPC port, HTTP fixture and data directory.
// It never connects to a configured deployment or Telegram account.
func TestIsolatedAria2RPCRecovery(t *testing.T) {
	binary := os.Getenv("TDL_ARIA2_E2E_BINARY")
	if binary == "" {
		t.Skip("set TDL_ARIA2_E2E_BINARY to run the isolated aria2 daemon test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	directory := t.TempDir()
	payload := bytes.Repeat([]byte("tdl-isolated-rpc-fixture\n"), 400000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(slowRPCFixtureWriter{w, r.Context()}, r, "fixture.bin", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	rpcURL, secret := fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port), rand.Text()
	session := filepath.Join(directory, "aria2.session")
	require.NoError(t, os.WriteFile(session, nil, 0o600))
	output, err := os.Create(filepath.Join(directory, "aria2.log"))
	require.NoError(t, err)
	defer output.Close()
	var daemon *exec.Cmd
	stop := func() {
		if daemon != nil {
			_ = daemon.Process.Kill()
			_ = daemon.Wait()
			daemon = nil
		}
	}
	defer stop()
	start := func() {
		daemon = exec.CommandContext(ctx, binary,
			"--enable-rpc=true", "--rpc-listen-all=false", "--disable-ipv6=true",
			"--rpc-listen-port="+strconv.Itoa(port), "--rpc-secret="+secret,
			"--dir="+directory, "--save-session="+session, "--input-file="+session,
			"--save-session-interval=1", "--continue=true", "--auto-file-renaming=false",
			"--max-concurrent-downloads=1", "--split=1", "--max-connection-per-server=1",
			"--file-allocation=none", "--console-log-level=error", "--enable-color=false")
		daemon.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		daemon.Stdout, daemon.Stderr = output, output
		require.NoError(t, daemon.Start())
	}
	client := aria2rpc.NewClient(types.Aria2Config{RPCURL: rpcURL, Secret: secret, TimeoutSeconds: 1})
	ready := func() bool { _, callErr := client.GetGlobalOptions(ctx); return callErr == nil }
	start()
	require.Eventually(t, ready, 10*time.Second, 100*time.Millisecond)
	engine, err := kv.New(kv.DriverBolt, filepath.Join(directory, "store"))
	require.NoError(t, err)
	defer engine.Close()
	store, err := engine.Open(string(types.DefaultAccount))
	require.NoError(t, err)
	repo := taskhub.NewAria2Repository(store)
	options := aria2.Options{Account: types.DefaultAccount, Client: client, Store: repo, PublicBaseURL: server.URL, Limit: 1, Connections: 1}
	controller := aria2.NewController(options, nil)
	result, err := controller.Submit(ctx, types.DownloadSubmission{Account: types.DefaultAccount, TaskID: "rpc-fixture", DownloadURL: server.URL + "/download/fixture", Dir: directory, Out: "fixture.bin"})
	require.NoError(t, err)
	statusIs := func(want string) func() bool {
		return func() bool {
			task, callErr := client.TellStatus(ctx, result.ID)
			return callErr == nil && task.Status == want
		}
	}
	require.Eventually(t, statusIs(string(types.DownloadActive)), 5*time.Second, 20*time.Millisecond)
	require.NoError(t, controller.PauseTask(ctx, result.ID))
	require.Eventually(t, statusIs(string(types.DownloadPaused)), 5*time.Second, 20*time.Millisecond)
	_, err = aria2rpc.Call(ctx, &http.Client{Timeout: time.Second}, rpcURL, secret, "aria2.saveSession", nil, 1)
	require.NoError(t, err)
	stop()
	_, err = client.GetGlobalOptions(ctx)
	require.Error(t, err, "isolated transport must actually be unavailable")
	registry := rte.NewRegistry()
	require.NoError(t, aria2.Register(registry, aria2.NewManager(options, nil), nil))
	host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{aria2.ID: {
		"connect_retry_ms": 100, "connect_retry_max_ms": 200, "status_interval_ms": 100, "monitor_poll_ms": 3600000,
	}})
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		require.NoError(t, host.Stop(stopCtx))
	}()
	start()
	require.Eventually(t, ready, 10*time.Second, 100*time.Millisecond)
	// A manager recovering from an unavailable transport must honor user pauses.
	require.Eventually(t, func() bool {
		records, readErr := repo.Records(ctx)
		return readErr == nil && records[result.ID].Revision > 4
	}, 5*time.Second, 50*time.Millisecond)
	require.True(t, statusIs(string(types.DownloadPaused))(), "recovery resumed a user-paused task")
	require.NoError(t, controller.UnpauseTask(ctx, result.ID))
	require.Eventually(t, statusIs(string(types.DownloadComplete)), 30*time.Second, 50*time.Millisecond)
	actual, err := os.ReadFile(filepath.Join(directory, "fixture.bin"))
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(payload), sha256.Sum256(actual))
	require.NoError(t, controller.RemoveTask(ctx, result.ID))
	require.NoError(t, controller.SyncStates(ctx))
	records, err := repo.Records(ctx)
	require.NoError(t, err)
	require.NotContains(t, records, result.ID)
	t.Log("isolated aria2 disconnect/restart, manual pause, resume, SHA-256 and deletion passed")
}

type slowRPCFixtureWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w slowRPCFixtureWriter) Write(data []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return w.ResponseWriter.Write(data)
	}
}
