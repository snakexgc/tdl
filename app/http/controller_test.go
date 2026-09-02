package httpdl

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
)

func TestControllerCanRestartHTTPService(t *testing.T) {
	port := reserveTCPPort(t)
	cfg := config.DefaultConfig()
	cfg.HTTP.Address = "127.0.0.1"
	cfg.HTTP.Port = port

	service := NewService(cfg, nil, nil)
	controller := NewController(context.Background(), service)

	for range 2 {
		require.True(t, controller.Start())
		require.Eventually(t, func() bool {
			conn, err := net.DialTimeout("tcp", config.HTTPListenAddr(cfg), 100*time.Millisecond)
			if err != nil {
				return false
			}
			_ = conn.Close()
			return true
		}, time.Second, 10*time.Millisecond)
		require.True(t, controller.Running())

		controller.Stop()
		require.False(t, controller.Running())
		require.NoError(t, controller.LastError())
	}
}

func TestControllerReportsListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	cfg := config.DefaultConfig()
	cfg.HTTP.Address = "127.0.0.1"
	cfg.HTTP.Port = port
	controller := NewController(context.Background(), NewService(cfg, nil, nil))

	require.True(t, controller.Start())
	require.Eventually(t, func() bool { return !controller.Running() }, time.Second, 10*time.Millisecond)
	require.Error(t, controller.LastError())
}

func TestServiceUpdatesHTTPConfigWithoutReplacingSharedState(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.HTTP.PublicBaseURL = "http://old.example"
	service := NewService(cfg, nil, nil)
	proxy := service.Proxy()
	tasks := proxy.Tasks()
	pools := service.Pools()

	next := config.DefaultConfig()
	next.HTTP.PublicBaseURL = "http://new.example/base"
	next.HTTP.Port++
	require.True(t, service.UpdateConfig(next))

	got, err := proxy.BuildURL("task-id")
	require.NoError(t, err)
	require.Equal(t, "http://new.example/base/download/task-id", got)
	require.Same(t, tasks, proxy.Tasks())
	require.Same(t, pools, service.Pools())
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}
