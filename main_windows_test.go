package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/types"
	bootstrap "github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
)

// Exercise the actual Windows console event in a hidden, isolated console.
// The child runs main with temporary configuration and no external services.
func TestConsoleShutdown(t *testing.T) {
	const helperEnv = "TDL_TEST_CONSOLE_SHUTDOWN"
	const interruptMode = "interrupt"
	const closeMode = "close"
	if mode := os.Getenv(helperEnv); mode != "" {
		directory := os.Getenv(consts.EnvHome)
		kernel := syscall.NewLazyDLL("kernel32.dll")
		window, _, err := kernel.NewProc("GetConsoleWindow").Call()
		require.NotZero(t, window, "GetConsoleWindow: %v", err)
		require.NoError(t, os.WriteFile(filepath.Join(directory, "window"), []byte(strconv.FormatUint(uint64(window), 10)), 0o600))
		if mode == interruptMode {
			go func() {
				for {
					if _, err := os.Stat(filepath.Join(directory, interruptMode)); err == nil {
						// Group zero targets only this child's separate console.
						_, _, _ = kernel.NewProc("GenerateConsoleCtrlEvent").Call(0, 0)
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
			}()
		}
		os.Args = []string{os.Args[0]}
		main()
		// Verify that main released storage and preserved task progress before
		// process exit, rather than relying on Windows to release file handles.
		engine, err := kv.New(kv.DriverBolt, filepath.Join(directory, ".tdl"))
		require.NoError(t, err)
		store, err := engine.Open("default")
		require.NoError(t, err)
		if mode != closeMode {
			record, found, err := taskhub.NewLocalRepository(store).Get(context.Background(), "shutdown-fixture")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, types.LocalDownloadStatusPaused, record.Status)
			require.EqualValues(t, 123, record.Completed)
		}
		require.NoError(t, engine.Close())
		require.NoError(t, os.WriteFile(filepath.Join(directory, "exited"), []byte("clean"), 0o600))
		return
	}

	for _, mode := range []string{closeMode, "close-with-watcher", interruptMode} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			address := configureConsoleTest(t, directory, mode != closeMode)
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestConsoleShutdown$")
			child.Env = append(os.Environ(), helperEnv+"="+mode, consts.EnvHome+"="+directory)
			child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010, HideWindow: true} // CREATE_NEW_CONSOLE
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			require.NoError(t, child.Start())
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = child.Process.Kill()
					<-done
				}
			})
			client := &http.Client{Timeout: 200 * time.Millisecond}
			defer client.CloseIdleConnections()
			logPath := filepath.Join(directory, ".tdl", "log", "latest.log")
			require.Eventually(t, func() bool {
				response, err := client.Get("http://" + address + "/login")
				if err != nil {
					return false
				}
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					return false
				}
				if mode != closeMode {
					logs, _ := os.ReadFile(logPath)
					return strings.Contains(string(logs), "Telegram 监听已断开，稍后重连")
				}
				return true
			}, 8*time.Second, 20*time.Millisecond)
			started := time.Now()
			if mode == interruptMode {
				require.NoError(t, os.WriteFile(filepath.Join(directory, interruptMode), nil, 0o600))
			} else {
				data, err := os.ReadFile(filepath.Join(directory, "window"))
				require.NoError(t, err)
				window, err := strconv.ParseUint(string(data), 10, 64)
				require.NoError(t, err)
				ok, _, callErr := syscall.NewLazyDLL("user32.dll").NewProc("PostMessageW").Call(uintptr(window), 0x0010, 0, 0) // WM_CLOSE
				require.NotZero(t, ok, "PostMessageW: %v", callErr)
			}
			select {
			case err := <-done:
				waited = true
				require.NoError(t, err, output.String())
			case <-ctx.Done():
				t.Fatal("console shutdown did not finish")
			}
			t.Logf("%s exited normally in %s", mode, time.Since(started))
			require.FileExists(t, filepath.Join(directory, "exited"), "main must return before Windows terminates the process")
			logs, err := os.ReadFile(logPath)
			require.NoError(t, err)
			require.Contains(t, string(logs), "TDL 已停止")
			require.Contains(t, output.String(), "正在停止 TDL")
			require.Contains(t, output.String(), "✓ TDL 已停止")
			require.NotContains(t, string(logs), "stop component resources; instances retained for retry")
		})
	}
}

func configureConsoleTest(t *testing.T, directory string, watcher bool) string {
	t.Helper()
	ctx := context.Background()
	service, err := bootstrap.Open(ctx, directory)
	require.NoError(t, err)
	catalog, err := application.Catalog()
	require.NoError(t, err)
	settings := rte.NewDirectory(catalog, service.Store())
	for _, id := range []string{"time.sync", "console.bot", "downloader.aria2", "downloader.local", "forwarder", "trigger.download", "trigger.forward"} {
		require.NoError(t, settings.SetEnabled(ctx, id, false))
	}
	address := ""
	for _, id := range []string{"panel.webui", "proxy.range"} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := listener.Addr().(*net.TCPAddr).Port
		require.NoError(t, listener.Close())
		require.NoError(t, settings.Patch(ctx, id, map[string]any{"address": "127.0.0.1", "port": port}))
		if id == "panel.webui" {
			address = fmt.Sprintf("127.0.0.1:%d", port)
		}
	}
	if watcher {
		require.NoError(t, settings.SetEnabled(ctx, "trigger.download", true))
		engine, err := kv.New(kv.DriverBolt, filepath.Join(directory, ".tdl"))
		require.NoError(t, err)
		defer engine.Close()
		store, err := engine.Open("default")
		require.NoError(t, err)
		// Missing credential identity fails validation before any Telegram call.
		require.NoError(t, store.Set(ctx, tgauth.SessionKey, []byte("isolated-test-session")))
		require.NoError(t, taskhub.NewLocalRepository(store).Save(ctx, types.LocalDownloadRecord{
			ID: "shutdown-fixture", Status: types.LocalDownloadStatusActive, Completed: 123, Total: 1024,
		}))
	}
	return address
}
