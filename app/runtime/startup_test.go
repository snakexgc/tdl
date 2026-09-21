package runtime

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
)

const (
	testHTTPResource  = "http"
	testPanelResource = "panel"
)

type offlineStartupSession struct{ calls atomic.Int32 }

func (s *offlineStartupSession) Check(context.Context, types.AccountID) (*ports.AccountIdentity, error) {
	s.calls.Add(1)
	return nil, &net.DNSError{Err: "network unavailable", IsTimeout: true}
}

func TestWatchStartupDoesNotRequireOnlinePreflight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.DefaultConfig()
	engine, err := kv.New(kv.DriverFile, filepath.Join(t.TempDir(), "state"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	namespace, err := engine.Open(cfg.Namespace)
	require.NoError(t, err)
	// Missing credential identity causes local validation to fail in each watch
	// attempt. This exercises the reconnect owner without contacting Telegram.
	require.NoError(t, namespace.Set(ctx, tgauth.SessionKey, []byte("test-session")))
	store := newStoppedComponentStore(t)
	saveComponent(t, store, "trigger.download", true, nil)
	m := NewManager(kv.With(config.WithSource(ctx, config.NewSource(cfg)), engine), engine, namespace, Options{ComponentStore: store})
	t.Cleanup(m.Shutdown)
	session := new(offlineStartupSession)
	m.sessionPort = session
	started := make(chan error, 1)
	go func() { started <- m.StartWatch(ctx) }()
	select {
	case err := <-started:
		require.NoError(t, err, "an offline preflight must not prevent the reconnect worker from starting")
	case <-time.After(time.Second):
		t.Fatal("watch startup blocked on network")
	}
	require.Zero(t, session.calls.Load())
	require.True(t, m.watchCtrl.Running())
	stopCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	require.NoError(t, m.watchCtrl.StopContext(stopCtx))
}

func TestWebUIStartupReportsActualListenerReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	store := newStoppedComponentStore(t)
	saveComponent(t, store, panelComponentID, true, map[string]any{
		"address": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port,
		"username": "admin", "password": "startup-test",
	})
	m := NewManager(config.WithSource(ctx, config.NewSource(config.DefaultConfig())), nil, nil, Options{ComponentStore: store})
	t.Cleanup(m.Shutdown)
	require.False(t, m.StartWebUI(ctx), "an occupied port must not be reported as ready")
	require.NoError(t, m.panelProcess.Stop(context.Background()))
	require.NoError(t, listener.Close())
	require.True(t, m.StartWebUI(ctx))
	require.True(t, m.StartWebUI(ctx), "starting an already ready panel is idempotent")
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + address + "/login")
	require.NoError(t, err, "success must mean the listener is bound, without sleeping")
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
}

func TestBackendStartupFailuresDoNotStopWebUI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	store := newStoppedComponentStore(t)
	saveComponent(t, store, panelComponentID, true, map[string]any{"address": "127.0.0.1", "port": port})
	m := NewManager(config.WithSource(ctx, config.NewSource(config.DefaultConfig())), nil, nil, Options{ComponentStore: store})
	t.Cleanup(m.Shutdown)
	require.True(t, m.StartWebUI(ctx))
	units := m.managedUnits(config.From(m.parent))
	entered, release := make(chan struct{}), make(chan struct{})
	networkError := &net.DNSError{Err: "network unavailable", Name: "offline.invalid", IsTimeout: true}
	for i := range units {
		switch units[i].ID {
		case moduleIDAria2, moduleIDBot, moduleIDWatch, testHTTPResource:
			units[i].Enabled = true
			units[i].Start = func(context.Context) error { return networkError }
			if units[i].ID == moduleIDAria2 {
				units[i].Start = func(context.Context) error {
					close(entered)
					<-release
					return networkError
				}
			}
		}
	}
	done := make(chan error, 1)
	go func() { done <- m.reconciler.Reconcile(ctx, units) }()
	// Release the intentionally stalled startup before draining the manager,
	// including when an assertion below fails.
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("backend startup did not begin")
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	check := func() {
		t.Helper()
		response, err := client.Get("http://" + address + "/login")
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.True(t, m.panelProcess.Running())
	}
	check()
	close(release)
	released = true
	select {
	case err := <-done:
		require.ErrorIs(t, err, networkError)
	case <-time.After(5 * time.Second):
		t.Fatal("failed backends did not finish startup")
	}
	check()
	for _, component := range m.reconciler.Health().Components {
		switch component.ID {
		case moduleIDAria2, moduleIDBot, moduleIDWatch, testHTTPResource:
			require.Equal(t, rte.Failed, component.State, component.ID)
		case testPanelResource:
			require.Equal(t, rte.Running, component.State)
		}
	}
}
