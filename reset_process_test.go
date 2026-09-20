package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/migration"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
)

const resetHelperEnvironment = "TDL_RESET_PROCESS_TEST"

func TestResetProcessHelper(t *testing.T) {
	if os.Getenv(resetHelperEnvironment) != "1" {
		return
	}
	os.Args = os.Args[:1]
	main()
}

// Exercise the real entry point, shutdown graph, Bolt database, rotating logger
// and HTTP confirmation. The child process is confined to a disposable TDL_HOME.
func TestFullResetStopsProcessAndClearsItsDisposableHome(t *testing.T) {
	home := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	cfg.Modules.WebUI = true
	cfg.WebUI.Address, cfg.WebUI.Port = "127.0.0.1", port
	require.NoError(t, config.Save(filepath.Join(home, "config.json"), cfg))
	_, err = migration.EnsureComponents(context.Background(), home, cfg)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(home, ".tdl"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".tdl", "another-account-fixture"), []byte("session fixture"), 0o600))
	keep := filepath.Join(home, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("unrelated file"), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResetProcessHelper$")
	child.Env = append(os.Environ(), consts.EnvHome+"="+home, resetHelperEnvironment+"=1")
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	require.NoError(t, child.Start())
	finished := make(chan struct{})
	var waitErr error
	go func() { waitErr = child.Wait(); close(finished) }()
	t.Cleanup(func() { cancel(); <-finished })
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, Timeout: time.Second}
	base := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		response, err := client.Get(base + "/api/auth/session")
		if err == nil {
			_ = response.Body.Close()
			break
		}
		select {
		case <-finished:
			t.Fatalf("process exited before WebUI started: %v\n%s", waitErr, output.String())
		case <-ctx.Done():
			t.Fatal("WebUI startup timed out")
		case <-time.After(50 * time.Millisecond):
		}
	}
	post := func(path, body string) (int, string) {
		response, err := client.Post(base+path, "application/json", strings.NewReader(body))
		require.NoError(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(data)
	}
	status, body := post("/api/auth/login", `{"username":"admin","password":"admin"}`)
	require.Equal(t, http.StatusOK, status, body)
	status, body = post("/api/system/reset", `{"confirmation":"RESET_TDL"}`)
	require.Equal(t, http.StatusAccepted, status, body)
	select {
	case <-finished:
		require.NoError(t, waitErr, output.String())
	case <-ctx.Done():
		t.Fatal("reset did not stop the process")
	}
	require.Contains(t, output.String(), "Reset complete")
	require.NoFileExists(t, filepath.Join(home, "config.json"))
	for _, name := range []string{".tdl", "components"} {
		entries, err := os.ReadDir(filepath.Join(home, name))
		require.NoError(t, err)
		require.Empty(t, entries, "no files may be recreated after cleanup")
	}
	require.FileExists(t, keep)
}
